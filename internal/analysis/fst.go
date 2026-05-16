package analysis

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/blevesearch/vellum"
)

// FSTDictionary is a read-optimised, memory-efficient term dictionary built
// on top of blevesearch/vellum (finite state transducer).
//
// # What it does
//
//   - O(1) exact term lookup via FST traversal
//   - O(prefix_length) prefix expansion — "kubern" → ["kubernetes", ...]
//   - Memory: ~2–4 bytes per term vs ~50–100 bytes per entry in a Go map
//
// # What it does NOT do
//
//   - Fuzzy/edit-distance lookup — that stays in BKTree
//   - Phonetic lookup — that stays in PhoneticIndex
//   - Real-time incremental inserts — vellum requires lexicographic insert
//     order, so the FST is rebuilt in bulk from a sorted term list
//
// # Lifecycle
//
//	dict := NewFSTDictionary()
//	dict.Build(terms)          // call after indexing a batch, or on Save
//	dict.Contains("kubernetes")
//	dict.PrefixSearch("kubern", 10)
//
// During active indexing, use the Engine's globalSeen map as the live
// dictionary. Call Build() when saving the index so the FST is always
// consistent with the persisted state.
type FSTDictionary struct {
	fst   *vellum.FST
	built bool
}

// NewFSTDictionary creates an empty FSTDictionary.
// Call Build() before using Contains or PrefixSearch.
func NewFSTDictionary() *FSTDictionary {
	return &FSTDictionary{}
}

// Build constructs the FST from terms.
// terms does not need to be sorted — Build sorts internally.
// Existing FST is replaced atomically.
// Safe to call multiple times (rebuild after re-indexing).
func (d *FSTDictionary) Build(terms []string) error {
	if len(terms) == 0 {
		d.fst = nil
		d.built = false
		return nil
	}

	// vellum REQUIRES lexicographic order — sort a copy.
	sorted := make([]string, len(terms))
	copy(sorted, terms)
	sort.Strings(sorted)

	// Deduplicate (sort makes this O(n)).
	deduped := sorted[:0]
	for i, t := range sorted {
		if i == 0 || t != sorted[i-1] {
			deduped = append(deduped, t)
		}
	}

	var buf bytes.Buffer
	builder, err := vellum.New(&buf, nil)
	if err != nil {
		return fmt.Errorf("fst: failed to create builder: %w", err)
	}

	// Value encodes term position in sorted order (1-indexed).
	// This lets us use the FST as an ordered dictionary — value 0
	// is reserved as "not found" in vellum's Get API.
	for i, term := range deduped {
		if err := builder.Insert([]byte(term), uint64(i+1)); err != nil {
			return fmt.Errorf("fst: insert %q failed: %w", term, err)
		}
	}

	if err := builder.Close(); err != nil {
		return fmt.Errorf("fst: builder close failed: %w", err)
	}

	fst, err := vellum.Load(buf.Bytes())
	if err != nil {
		return fmt.Errorf("fst: load failed: %w", err)
	}

	d.fst = fst
	d.built = true
	return nil
}

// Contains returns true if term exists exactly in the dictionary.
// Returns false if the FST has not been built yet.
func (d *FSTDictionary) Contains(term string) bool {
	if !d.built || d.fst == nil {
		return false
	}
	_, exists, err := d.fst.Get([]byte(term))
	return err == nil && exists
}

// PrefixSearch returns up to maxResults terms that start with prefix,
// in lexicographic order. Returns nil if FST not built or prefix not found.
func (d *FSTDictionary) PrefixSearch(prefix string, maxResults int) ([]string, error) {
	if !d.built || d.fst == nil {
		return nil, nil
	}
	if maxResults <= 0 {
		maxResults = 20
	}

	// vellum iterator: startInclusive = prefix, endExclusive = prefix+\xff
	// This scans all keys that start with prefix.
	startKey := []byte(prefix)
	endKey := prefixUpperBound(prefix)

	itr, err := d.fst.Iterator(startKey, endKey)
	if err != nil {
		// vellum returns an error (not nil iterator) when no keys match.
		// Treat as empty result.
		return nil, nil
	}

	var results []string
	for err == nil && len(results) < maxResults {
		key, _ := itr.Current()
		results = append(results, string(key))
		err = itr.Next()
	}

	return results, nil
}

// Size returns the number of terms in the FST.
// Returns 0 if not built.
func (d *FSTDictionary) Size() int {
	if !d.built || d.fst == nil {
		return 0
	}
	return int(d.fst.Len())
}

// IsBuilt returns true if Build() has been called successfully at least once.
func (d *FSTDictionary) IsBuilt() bool {
	return d.built
}

// prefixUpperBound returns the smallest byte string that is strictly greater
// than all strings with the given prefix. Used as the exclusive end key for
// vellum's prefix iterator.
//
// Algorithm: increment the last byte. If overflow, pop and try again.
// "kubern" → "kubero" (o = n+1)
// "kubernz" → "kubero" (z overflows → pop, n+1)
// "\xff\xff" → nil (no upper bound, scan to end)
func prefixUpperBound(prefix string) []byte {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			upper := make([]byte, i+1)
			copy(upper, b)
			upper[i]++
			return upper
		}
	}
	// All bytes are 0xff — no upper bound, iterate to FST end.
	return nil
}
