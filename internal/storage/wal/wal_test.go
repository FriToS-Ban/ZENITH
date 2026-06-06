package wal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openTestWAL(t *testing.T) (*WAL, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.wal")
	w, records, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected empty WAL on fresh open, got %d records", len(records))
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, path
}

// ─── Append ───────────────────────────────────────────────────────────────────

func TestWAL_Append_Put(t *testing.T) {
	w, _ := openTestWAL(t)
	ctx := context.Background()

	seq, err := w.Append(ctx, &Record{Op: OpTypePut, Key: []byte("key1"), Value: []byte("value1")})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if seq == 0 {
		t.Error("expected non-zero sequence number")
	}
}

func TestWAL_Append_Delete(t *testing.T) {
	w, _ := openTestWAL(t)
	ctx := context.Background()

	seq, err := w.Append(ctx, &Record{Op: OpTypeDelete, Key: []byte("key1")})
	if err != nil {
		t.Fatalf("Append delete: %v", err)
	}
	if seq == 0 {
		t.Error("expected non-zero sequence number for delete")
	}
}

func TestWAL_Append_SequenceMonotonic(t *testing.T) {
	w, _ := openTestWAL(t)
	ctx := context.Background()

	var prev uint64
	for i := range 10 {
		seq, err := w.Append(ctx, &Record{
			Op:    OpTypePut,
			Key:   []byte("k"),
			Value: []byte("v"),
		})
		if err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
		if seq <= prev {
			t.Errorf("sequence not monotonic: seq %d <= prev %d at iteration %d", seq, prev, i)
		}
		prev = seq
	}
}

func TestWAL_Append_InvalidRecord(t *testing.T) {
	w, _ := openTestWAL(t)
	ctx := context.Background()

	// Delete with a value is invalid.
	_, err := w.Append(ctx, &Record{Op: OpTypeDelete, Key: []byte("k"), Value: []byte("v")})
	if err == nil {
		t.Error("expected error for delete record with value")
	}

	// Invalid op type.
	_, err = w.Append(ctx, &Record{Op: OpType(99), Key: []byte("k")})
	if err == nil {
		t.Error("expected error for invalid op type")
	}
}

func TestWAL_Append_ClosedWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.wal")
	w, _, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	_, err = w.Append(context.Background(), &Record{Op: OpTypePut, Key: []byte("k"), Value: []byte("v")})
	if err == nil {
		t.Error("expected error appending to closed WAL")
	}
}

// ─── Recovery ─────────────────────────────────────────────────────────────────

func TestWAL_Recovery_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.wal")
	ctx := context.Background()

	// Write 5 records.
	w1, _, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ key, val string }{
		{"alpha", "1"},
		{"beta", "2"},
		{"gamma", "3"},
		{"delta", "4"},
		{"epsilon", "5"},
	}
	for _, r := range want {
		_, err := w1.Append(ctx, &Record{Op: OpTypePut, Key: []byte(r.key), Value: []byte(r.val)})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w1.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open and recover.
	w2, records, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	t.Cleanup(func() { _ = w2.Close() })
	if len(records) != len(want) {
		t.Fatalf("recovery: got %d records, want %d", len(records), len(want))
	}
	for i, r := range records {
		if string(r.Key) != want[i].key || string(r.Value) != want[i].val {
			t.Errorf("record %d: got key=%q val=%q, want key=%q val=%q",
				i, r.Key, r.Value, want[i].key, want[i].val)
		}
	}
}

func TestWAL_Recovery_CorruptTailTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.wal")
	ctx := context.Background()

	w, _, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Append(ctx, &Record{Op: OpTypePut, Key: []byte("good"), Value: []byte("record")})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Append garbage bytes to simulate a crash-corrupted tail.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("GARBAGE_BYTES_HERE_XYZ"))
	_ = f.Close()

	// Recovery should still return the valid record and not error.
	w2, records, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("recovery with corrupt tail: %v", err)
	}
	t.Cleanup(func() { _ = w2.Close() })
	if len(records) != 1 {
		t.Errorf("expected 1 valid record after corrupt tail, got %d", len(records))
	}
	if string(records[0].Key) != "good" {
		t.Errorf("wrong key after recovery: %q", records[0].Key)
	}
}

func TestWAL_Recovery_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.wal")
	w, records, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("empty WAL open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if len(records) != 0 {
		t.Errorf("expected 0 records from empty file, got %d", len(records))
	}
}

// ─── encodeRecord / decodeRecord ─────────────────────────────────────────────

func TestEncodeDecodeRecord_Roundtrip(t *testing.T) {
	cases := []Record{
		{Seq: 1, Op: OpTypePut, Key: []byte("hello"), Value: []byte("world")},
		{Seq: 99, Op: OpTypeDelete, Key: []byte("byekey")},
		{Seq: 0, Op: OpTypePut, Key: []byte("empty_val"), Value: []byte{}},
	}
	for _, orig := range cases {
		body, err := encodeRecord(&orig)
		if err != nil {
			t.Fatalf("encodeRecord: %v", err)
		}
		got, err := decodeRecord(body)
		if err != nil {
			t.Fatalf("decodeRecord: %v", err)
		}
		if string(got.Key) != string(orig.Key) {
			t.Errorf("key: got %q, want %q", got.Key, orig.Key)
		}
		if string(got.Value) != string(orig.Value) {
			t.Errorf("value: got %q, want %q", got.Value, orig.Value)
		}
		if got.Op != orig.Op {
			t.Errorf("op: got %d, want %d", got.Op, orig.Op)
		}
	}
}

func TestEncodeRecord_InvalidOp(t *testing.T) {
	_, err := encodeRecord(&Record{Op: OpType(255), Key: []byte("k")})
	if err == nil {
		t.Error("expected error for invalid op type")
	}
}

// ─── Reset ────────────────────────────────────────────────────────────────────

func TestWALReset_EmptiesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.wal")
	ctx := context.Background()

	w, _, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		_, _ = w.Append(ctx, &Record{Op: OpTypePut, Key: []byte("k"), Value: []byte{byte(i)}})
	}

	if err := w.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close after Reset: %v", err)
	}

	// Reopen — must have zero records.
	w2, records, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("reopen after Reset: %v", err)
	}
	t.Cleanup(func() { _ = w2.Close() })
	if len(records) != 0 {
		t.Fatalf("expected 0 records after Reset, got %d", len(records))
	}
}

func TestWALReset_AcceptsWritesAfterReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset_write.wal")
	ctx := context.Background()

	w, _, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Append(ctx, &Record{Op: OpTypePut, Key: []byte("before"), Value: []byte("1")})

	if err := w.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Write a new record after Reset — must succeed.
	if _, err := w.Append(ctx, &Record{Op: OpTypePut, Key: []byte("after"), Value: []byte("2")}); err != nil {
		t.Fatalf("Append after Reset: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen — must see only the post-Reset record.
	w2, records, err := OpenWAL(path, WALConfig{SyncMode: SyncAlways})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = w2.Close() })
	if len(records) != 1 {
		t.Fatalf("expected 1 record after Reset+Append, got %d", len(records))
	}
	if string(records[0].Key) != "after" {
		t.Errorf("expected key 'after', got %q", records[0].Key)
	}
}
