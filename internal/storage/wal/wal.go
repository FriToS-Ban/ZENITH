package wal

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Write Ahead Log

type SyncMode int

const (
	SyncAlways SyncMode = iota
	SyncPeriodic
	SyncGroupCommit
)

type CompressionCodec int

const (
	CodecNone CompressionCodec = iota
	CodecSnappy
	CodecZstd // developed by facebook better than gzip
)

type WALConfig struct {
	// durability
	SyncMode          SyncMode
	SyncInterval      time.Duration // for periodic
	GroupCommitWindow time.Duration // for group commit

	// storage
	MaxSegmentSize int64
	Dir            string

	// compression
	Codec     CompressionCodec
	ZstdLevel int
}

type WAL struct {

	cfg WALConfig
	
	mu sync.Mutex
	seq atomic.Uint64
	closed atomic.Bool

	file *os.File
	buf *bufio.Writer
	
	syncCh chan struct{} // stop signal 
	syncDone chan struct{} // background threads done
	byteWritten int64

}

type OpType byte

const (
	OpTypePut OpType = iota + 1
	OpTypeDelete
)

func (op OpType) Valid() bool {
	switch op {
	case OpTypePut, OpTypeDelete:
		return true
	default:
		return false
	}
}

type Record struct {
	Seq   uint64
	Op    OpType
	Key   []byte
	Value []byte
}

func (r *Record) Validate() error {
	if !r.Op.Valid() {
		return errors.New("invalid op type")
	}
	if r.Op == OpTypeDelete && len(r.Value) != 0 {
		return errors.New("delete should not have value")
	}
	return nil
}

func encodeRecord(r *Record) ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil record")
	}

	if err := r.Validate(); err != nil {
		return nil, err
	}

	if len(r.Key) > math.MaxUint32 {
		return nil, errors.New("key too large")
	}

	if len(r.Value) > math.MaxUint32 {
		return nil, errors.New("value too large")
	}

	const headerSize = 8 + 1 + 2 + 4 // seq + op + keylen + valuelen
	totalSize := headerSize + len(r.Key) + len(r.Value)

	buf := make([]byte, totalSize)

	offset := 0

	// seq
	binary.LittleEndian.PutUint64(buf[offset:], r.Seq)
	offset += 8

	// op
	buf[offset] = byte(r.Op)
	offset += 1

	// key length
	binary.LittleEndian.PutUint16(buf[offset:], uint16(len(r.Key)))
	offset += 2

	// value length
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(r.Value)))
	offset += 4

	// key
	copy(buf[offset:], r.Key)
	offset += len(r.Key)

	// value
	copy(buf[offset:], r.Value)

	return buf, nil
}

// [CRC][LENGTH][BODY]

const (
	crcSize   = 4
	lenSize   = 4
	headerSize = crcSize + lenSize
)


func buildEntry(body []byte) ([]byte,error) {

	if len(body) == 0 {
		return nil, errors.New("empty body")
	}

	if len(body) > math.MaxUint32 {
		return nil, errors.New("body too large")
	}

	// 4 bytes for crc, 4 bytes for length, body
	totalSize := headerSize + len(body)
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint32(buf[crcSize:], uint32(len(body)))

	copy(buf[headerSize:], body)

	// crc for length + body
	payload := buf[crcSize:]
	crc := crc32.ChecksumIEEE(payload)

	binary.LittleEndian.PutUint32(buf[0:], crc)

	return buf,nil
	
	
}

func decodeRecord(body []byte) (Record, error) {
	var r Record
	bo := 0

	if len(body) < 15 {
		return r, errors.New("body too small")
	}

	r.Seq = binary.LittleEndian.Uint64(body[bo:])
	bo += 8

	r.Op = OpType(body[bo])
	bo += 1

	keyLen := binary.LittleEndian.Uint16(body[bo:])
	bo += 2

	valLen := binary.LittleEndian.Uint32(body[bo:])
	bo += 4

	if int(keyLen)+int(valLen) != len(body)-bo {
		return r, errors.New("invalid key/value length")
	}

	r.Key = make([]byte, keyLen)
	copy(r.Key, body[bo:bo+int(keyLen)])
	bo += int(keyLen)

	r.Value = make([]byte, valLen)
	copy(r.Value, body[bo:bo+int(valLen)])

	if err := r.Validate(); err != nil {
		return r, err
	}

	return r, nil
}

func (w *WAL) Append(ctx context.Context, r *Record) (uint64, error) {
	
	if w.closed.Load() {
		return 0, errors.New("wal is closed")
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}


	r.Seq = w.seq.Add(1)

	body,err := encodeRecord(r)
	if err != nil {
		return 0, err
	}

	entry,err := buildEntry(body)
	if err != nil {
		return 0, err
	}

	w.mu.Lock()

	n,err:=w.buf.Write(entry)
	if err != nil {
		return 0, err
	}

	if n != len(entry) {
		return 0, errors.New("partial write")
	}

	w.byteWritten += int64(len(entry))

	needSync := w.cfg.SyncMode == SyncAlways

	w.mu.Unlock()

	if needSync {

		if err := w.buf.Flush(); err != nil {
			return 0, err
		}

		if err := w.file.Sync(); err != nil {
			return 0, err
		}

	}

	
	return r.Seq, nil
	
}

// []Record -> order of operation 
// int64 -> last valid sequence number (offset) truncates the corrupted data
// error -> i/o error or crc error

func Recover(file *os.File) ([]Record, int64, error) {
	var records []Record
	var offset int64 = 0

	reader := bufio.NewReader(file)

	for {
		// --- CRC ---
		crcBuf := make([]byte, crcSize)
		_, err := io.ReadFull(reader, crcBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return records, offset, nil
		}
		storedCRC := binary.LittleEndian.Uint32(crcBuf)

		// --- LENGTH ---
		lenBuf := make([]byte, lenSize)
		_, err = io.ReadFull(reader, lenBuf)
		if err != nil {
			return records, offset, nil
		}
		length := binary.LittleEndian.Uint32(lenBuf)

		if length == 0 || length > 10*1024*1024 {
			return records, offset, nil
		}

		// --- BODY ---
		body := make([]byte, length)
		_, err = io.ReadFull(reader, body)
		if err != nil {
			return records, offset, nil
		}

		// --- CRC CHECK ---
		h := crc32.NewIEEE()
		h.Write(lenBuf)
		h.Write(body)
		computedCRC := h.Sum32()

		if computedCRC != storedCRC {
			return records, offset, nil
		}

		// --- DECODE ---
		rec, err := decodeRecord(body)
		if err != nil {
			return records, offset, nil
		}

		records = append(records, rec)

		offset += int64(crcSize + lenSize + length)
	}

	return records, offset, nil
}