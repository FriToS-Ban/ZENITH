// Package corpus handles downloading, caching, and parsing the MS MARCO
// Passage Retrieval v1 dataset used for Recall@10 evaluation.
package corpus

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	collectionURL = "https://msmarco.z22.web.core.windows.net/msmarcoranking/collection.tar.gz"
	queriesURL    = "https://msmarco.z22.web.core.windows.net/msmarcoranking/queries.tar.gz"
	qrelsURL      = "https://msmarco.z22.web.core.windows.net/msmarcoranking/qrels.dev.small.tsv"
)

// Passage is a single indexed document from collection.tsv.
type Passage struct {
	ID   string
	Text string
}

// Query is a single evaluation query from queries.dev.small.tsv.
type Query struct {
	ID   string
	Text string
}

// Load downloads (if needed) and returns the first limit passages, all queries,
// and the qrels ground-truth map. cacheDir is created if it does not exist.
func Load(cacheDir string, limit int) (passages []Passage, queries []Query, qrels map[string]string, err error) {
	if err = os.MkdirAll(cacheDir, 0755); err != nil {
		return
	}

	collectionPath := filepath.Join(cacheDir, "collection.tsv")
	queriesPath := filepath.Join(cacheDir, "queries.dev.small.tsv")
	qrelsPath := filepath.Join(cacheDir, "qrels.dev.small.tsv")

	if err = ensureCollection(collectionPath); err != nil {
		return
	}
	// queries.tar.gz contains queries.dev.tsv (the full dev set, 6980 queries).
	if err = ensureTSVFromTar(queriesPath, queriesURL, "queries.dev.tsv"); err != nil {
		return
	}
	// qrels.dev.small.tsv is a plain TSV at the new domain (no compression).
	if err = ensurePlainTSV(qrelsPath, qrelsURL); err != nil {
		return
	}

	passages, err = loadPassages(collectionPath, limit)
	if err != nil {
		return
	}
	queries, err = loadQueries(queriesPath)
	if err != nil {
		return
	}
	qrels, err = loadQrels(qrelsPath)
	return
}

// SubsetIDs returns a set of passage IDs present in passages for qrel filtering.
func SubsetIDs(passages []Passage) map[string]struct{} {
	m := make(map[string]struct{}, len(passages))
	for _, p := range passages {
		m[p.ID] = struct{}{}
	}
	return m
}

// ensureCollection downloads and extracts collection.tar.gz if the TSV is absent.
// Writes to a .tmp file first and renames atomically on success so a dropped
// connection never leaves a corrupt file that looks valid on the next run.
func ensureCollection(tsvPath string) error {
	if fileExists(tsvPath) {
		return nil
	}
	fmt.Printf("Downloading collection.tar.gz (~987 MB compressed) — this only happens once...\n")
	resp, err := http.Get(collectionURL)
	if err != nil {
		return fmt.Errorf("corpus: download collection: %w", err)
	}
	defer resp.Body.Close()

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("corpus: gzip collection: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("corpus: tar collection: %w", err)
		}
		if filepath.Base(hdr.Name) == "collection.tsv" {
			tmp := tsvPath + ".tmp"
			out, err := os.Create(tmp)
			if err != nil {
				return fmt.Errorf("corpus: create collection.tsv.tmp: %w", err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				os.Remove(tmp)
				return fmt.Errorf("corpus: write collection.tsv: %w", err)
			}
			out.Close()
			if err := os.Rename(tmp, tsvPath); err != nil {
				os.Remove(tmp)
				return fmt.Errorf("corpus: rename collection.tsv: %w", err)
			}
			fmt.Println("collection.tsv cached.")
			return nil
		}
	}
	return fmt.Errorf("corpus: collection.tsv not found in tar archive")
}

// ensureTSVFromTar downloads a .tar.gz archive and extracts the named file from it.
func ensureTSVFromTar(tsvPath, url, entryName string) error {
	if fileExists(tsvPath) {
		return nil
	}
	fmt.Printf("Downloading %s (extracting %s)...\n", filepath.Base(url), entryName)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("corpus: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("corpus: gzip %s: %w", url, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("corpus: tar %s: %w", url, err)
		}
		if filepath.Base(hdr.Name) == entryName {
			tmp := tsvPath + ".tmp"
			out, err := os.Create(tmp)
			if err != nil {
				return fmt.Errorf("corpus: create %s.tmp: %w", tsvPath, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				os.Remove(tmp)
				return fmt.Errorf("corpus: write %s: %w", tsvPath, err)
			}
			out.Close()
			if err := os.Rename(tmp, tsvPath); err != nil {
				os.Remove(tmp)
				return fmt.Errorf("corpus: rename %s: %w", tsvPath, err)
			}
			fmt.Printf("%s cached.\n", entryName)
			return nil
		}
	}
	return fmt.Errorf("corpus: %s not found in %s", entryName, url)
}

// ensurePlainTSV downloads a plain (uncompressed) TSV file.
func ensurePlainTSV(tsvPath, url string) error {
	if fileExists(tsvPath) {
		return nil
	}
	fmt.Printf("Downloading %s...\n", filepath.Base(tsvPath))
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("corpus: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	tmp := tsvPath + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("corpus: create %s.tmp: %w", tsvPath, err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("corpus: write %s: %w", tsvPath, err)
	}
	out.Close()
	if err := os.Rename(tmp, tsvPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("corpus: rename %s: %w", tsvPath, err)
	}
	fmt.Printf("%s cached.\n", filepath.Base(tsvPath))
	return nil
}

func loadPassages(path string, limit int) ([]Passage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var passages []Passage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // 1 MB line buffer for long passages
	for sc.Scan() && (limit <= 0 || len(passages) < limit) {
		parts := strings.SplitN(sc.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		passages = append(passages, Passage{ID: parts[0], Text: parts[1]})
	}
	return passages, sc.Err()
}

func loadQueries(path string) ([]Query, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var queries []Query
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		queries = append(queries, Query{ID: parts[0], Text: parts[1]})
	}
	return queries, sc.Err()
}

func loadQrels(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	qrels := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// format: qid \t 0 \t pid \t 1
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) < 4 {
			continue
		}
		rel, _ := strconv.Atoi(parts[3])
		if rel > 0 {
			qrels[parts[0]] = parts[2]
		}
	}
	return qrels, sc.Err()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
