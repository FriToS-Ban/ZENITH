// Command lexdiag evaluates the lexical engines (SQLite FTS5, Bleve) on the
// gt-augmented MS MARCO corpus: the first 100k passages plus every
// ground-truth passage for the 6,980 qrel dev queries. This makes all 6,980
// queries evaluable, against the official benchmark's 32-query denominator.
//
// ZENITH's BM25 lexical list measures 0.803 Recall@10 on this corpus
// (internal/index/msmarco_diag_test.go); this command produces the directly
// comparable numbers for the competition.
//
// Usage: go run ./cmd/lexdiag [--cache=.cache]
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shramanb113/ZENITH/bench/internal/engines"
)

func main() {
	cacheDir := flag.String("cache", ".cache", "directory with cached MS MARCO files")
	flag.Parse()

	docs, queries, qrels, err := loadAugmented(*cacheDir, 100_000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("corpus: %d docs (100k + gt-augmented), %d evaluable queries\n", len(docs), len(queries))

	run("SQLite FTS5", func() (engines.Engine, error) { return engines.NewSQLiteEngine() }, docs, queries, qrels)
	run("Bleve", func() (engines.Engine, error) { return engines.NewBleveEngine() }, docs, queries, qrels)
}

func run(name string, newFn func() (engines.Engine, error), docs map[string]string, queries []query, qrels map[string]string) {
	eng, err := newFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] init: %v\n", name, err)
		return
	}
	defer eng.Close()

	ctx := context.Background()
	start := time.Now()
	if err := eng.IndexBatch(ctx, docs); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] index: %v\n", name, err)
		return
	}
	fmt.Printf("[%s] indexed %d docs in %s\n", name, len(docs), time.Since(start).Round(time.Millisecond))

	hits := 0
	start = time.Now()
	for _, q := range queries {
		ids, err := eng.Search(ctx, q.text, 10)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] query %s: %v\n", name, q.id, err)
			continue
		}
		gt := qrels[q.id]
		for _, id := range ids {
			if id == gt {
				hits++
				break
			}
		}
	}
	fmt.Printf("[%s] Recall@10 = %.4f (%d/%d)  query time %s\n",
		name, float64(hits)/float64(len(queries)), hits, len(queries), time.Since(start).Round(time.Millisecond))
}

type query struct{ id, text string }

func loadAugmented(dir string, nDocs int) (map[string]string, []query, map[string]string, error) {
	qrels := make(map[string]string)
	gtSet := make(map[string]struct{})
	if err := scanTSV(dir+`/qrels.dev.small.tsv`, func(p []string) bool {
		if len(p) >= 3 {
			qrels[p[0]] = p[2]
			gtSet[p[2]] = struct{}{}
		}
		return true
	}); err != nil {
		return nil, nil, nil, err
	}

	docs := make(map[string]string, nDocs+len(gtSet))
	found, base := 0, 0
	if err := scanTSV(dir+`/collection.tsv`, func(p []string) bool {
		if len(p) >= 2 {
			_, isGT := gtSet[p[0]]
			if base < nDocs {
				docs[p[0]] = p[1]
				base++
				if isGT {
					found++
				}
			} else if isGT {
				docs[p[0]] = p[1]
				found++
			}
		}
		return found < len(gtSet)
	}); err != nil {
		return nil, nil, nil, err
	}

	var queries []query
	if err := scanTSV(dir+`/queries.dev.small.tsv`, func(p []string) bool {
		if len(p) >= 2 {
			if pid, ok := qrels[p[0]]; ok {
				if _, in := docs[pid]; in {
					queries = append(queries, query{id: p[0], text: p[1]})
				}
			}
		}
		return true
	}); err != nil {
		return nil, nil, nil, err
	}
	return docs, queries, qrels, nil
}

func scanTSV(path string, fn func(parts []string) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if !fn(strings.Split(sc.Text(), "\t")) {
			break
		}
	}
	return sc.Err()
}
