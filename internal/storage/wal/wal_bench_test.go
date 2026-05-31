package wal_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shramanb113/ZENITH/internal/storage/wal"
)

// openFreshWAL creates a WAL in a temp directory and returns it with a cleanup func.
func openFreshWAL(tb testing.TB) (*wal.WAL, func()) {
	tb.Helper()
	dir, err := os.MkdirTemp("", "zenith-wal-bench-*")
	if err != nil {
		tb.Fatalf("MkdirTemp: %v", err)
	}
	path := filepath.Join(dir, "bench.wal")
	w, _, err := wal.OpenWAL(path, wal.WALConfig{SyncMode: wal.SyncAlways})
	if err != nil {
		os.RemoveAll(dir)
		tb.Fatalf("OpenWAL: %v", err)
	}
	return w, func() {
		w.Close()
		os.RemoveAll(dir)
	}
}

// writeNRecords writes n Put records to w using incrementing keys/values.
func writeNRecords(tb testing.TB, w *wal.WAL, n int) {
	tb.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("key-%010d", i))
		val := []byte(fmt.Sprintf("value-%010d", i))
		if _, err := w.Append(ctx, &wal.Record{Op: wal.OpTypePut, Key: key, Value: val}); err != nil {
			tb.Fatalf("Append: %v", err)
		}
	}
}

// prepareWALFile writes n records to a fresh WAL, closes it, and returns the
// path and cleanup func.
func prepareWALFile(tb testing.TB, n int) (string, func()) {
	tb.Helper()
	dir, err := os.MkdirTemp("", "zenith-wal-recovery-*")
	if err != nil {
		tb.Fatalf("MkdirTemp: %v", err)
	}
	path := filepath.Join(dir, "recovery.wal")
	w, _, err := wal.OpenWAL(path, wal.WALConfig{SyncMode: wal.SyncAlways})
	if err != nil {
		os.RemoveAll(dir)
		tb.Fatalf("OpenWAL: %v", err)
	}
	writeNRecords(tb, w, n)
	if err := w.Close(); err != nil {
		os.RemoveAll(dir)
		tb.Fatalf("Close: %v", err)
	}
	return path, func() { os.RemoveAll(dir) }
}

// ─── Append benchmarks ────────────────────────────────────────────────────────

// BenchmarkWALAppend measures sequential WAL append throughput.
func BenchmarkWALAppend(b *testing.B) {
	w, cleanup := openFreshWAL(b)
	defer cleanup()

	ctx := context.Background()
	key := []byte("bench-key-0000000001")
	val := []byte("bench-value-0000000001")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := w.Append(ctx, &wal.Record{Op: wal.OpTypePut, Key: key, Value: val}); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}
}

// BenchmarkWALAppendParallel measures concurrent WAL append throughput.
// The WAL serialises appends with a mutex, so this exercises lock contention.
func BenchmarkWALAppendParallel(b *testing.B) {
	w, cleanup := openFreshWAL(b)
	defer cleanup()

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		key := []byte("bench-key-parallel")
		val := []byte("bench-value-parallel")
		for pb.Next() {
			if _, err := w.Append(ctx, &wal.Record{Op: wal.OpTypePut, Key: key, Value: val}); err != nil {
				b.Fatalf("Append: %v", err)
			}
		}
	})
}

// BenchmarkWALAppendMixed measures sequential write throughput with a realistic
// mix of Put (90%) and Delete (10%) operations.
func BenchmarkWALAppendMixed(b *testing.B) {
	w, cleanup := openFreshWAL(b)
	defer cleanup()

	ctx := context.Background()
	key := []byte("bench-key-mixed")
	val := []byte("bench-value-mixed")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var r *wal.Record
		if i%10 == 0 {
			r = &wal.Record{Op: wal.OpTypeDelete, Key: key}
		} else {
			r = &wal.Record{Op: wal.OpTypePut, Key: key, Value: val}
		}
		if _, err := w.Append(ctx, r); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}
}

// ─── Recovery benchmarks ─────────────────────────────────────────────────────

func benchmarkRecovery(b *testing.B, recordCount int) {
	b.Helper()

	path, cleanup := prepareWALFile(b, recordCount)
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, records, err := wal.OpenWAL(path, wal.WALConfig{SyncMode: wal.SyncAlways})
		if err != nil {
			b.Fatalf("OpenWAL (recovery): %v", err)
		}
		if len(records) != recordCount {
			b.Fatalf("expected %d records, got %d", recordCount, len(records))
		}
		w.Close()
	}
}

// BenchmarkWALRecovery1K measures WAL recovery time for 1 000 records.
func BenchmarkWALRecovery1K(b *testing.B)   { benchmarkRecovery(b, 1_000) }

// BenchmarkWALRecovery10K measures WAL recovery time for 10 000 records.
func BenchmarkWALRecovery10K(b *testing.B) { benchmarkRecovery(b, 10_000) }

// BenchmarkWALRecovery100K measures WAL recovery time for 100 000 records.
func BenchmarkWALRecovery100K(b *testing.B) { benchmarkRecovery(b, 100_000) }

// ─── Concurrent recovery stress ───────────────────────────────────────────────

// BenchmarkWALRecoveryConcurrent writes records from N goroutines then measures
// recovery. This validates that recovery handles interleaved sequence numbers.
func BenchmarkWALRecoveryConcurrent(b *testing.B) {
	const goroutines = 8
	const recordsPerGoroutine = 500

	dir, err := os.MkdirTemp("", "zenith-wal-concurrent-*")
	if err != nil {
		b.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "concurrent.wal")
	w, _, err := wal.OpenWAL(path, wal.WALConfig{SyncMode: wal.SyncAlways})
	if err != nil {
		b.Fatalf("OpenWAL: %v", err)
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				key := []byte(fmt.Sprintf("g%d-key-%06d", id, i))
				val := []byte(fmt.Sprintf("g%d-val-%06d", id, i))
				if _, err := w.Append(ctx, &wal.Record{Op: wal.OpTypePut, Key: key, Value: val}); err != nil {
					// WAL append is serialised — errors here are fatal.
					panic(fmt.Sprintf("goroutine %d Append: %v", id, err))
				}
			}
		}(g)
	}
	wg.Wait()
	w.Close()

	total := goroutines * recordsPerGoroutine
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw, records, err := wal.OpenWAL(path, wal.WALConfig{SyncMode: wal.SyncAlways})
		if err != nil {
			b.Fatalf("OpenWAL (recovery): %v", err)
		}
		if len(records) != total {
			b.Fatalf("expected %d records, got %d", total, len(records))
		}
		rw.Close()
	}
}
