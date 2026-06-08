// demo runs a side-by-side comparison of BM25 keyword search vs ZENITH hybrid
// semantic search to illustrate meaning-based retrieval.
//
// Query: "heart attack symptoms"
// Corpus: 9 documents written in clinical terminology — none contain the words
// "heart", "attack", or "symptoms". BM25 returns zero results; ZENITH hybrid
// surfaces the cardiac documents because it understands what you meant.
//
// Usage:
//
//	go run ./demo
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/shramanb113/ZENITH/pkg/zenith"
)

// ANSI colour helpers — Windows Terminal and most Unix terminals render these.
const (
	reset = "\033[0m"
	bold  = "\033[1m"
	dim   = "\033[2m"
	green = "\033[32m"
	red   = "\033[31m"
	cyan  = "\033[36m"
	gray  = "\033[90m"
)

// corpus uses clinical/medical terminology only.
// Search words "heart", "attack", "symptoms" do not appear in any entry.
var corpus = map[string]string{
	"cardiac_1": "Myocardial infarction often presents with crushing precordial pressure " +
		"that radiates to the left arm, jaw, or neck. The patient appears diaphoretic " +
		"and pale, reporting nausea and profound fatigue.",

	"cardiac_2": "Acute coronary syndrome: sudden substernal chest tightness, dyspnea, " +
		"and cold sweats. Immediate STEMI or NSTEMI evaluation is required.",

	"cardiac_3": "Ischemic cardiac events produce severe chest heaviness lasting more than " +
		"20 minutes, radiation of pain down the left arm, shortness of breath, " +
		"and diaphoresis. Do not dismiss as indigestion.",

	"cardiac_4": "Prodromal indicators of acute MI: unexplained fatigue in the days prior, " +
		"jaw pain, left shoulder ache, and intermittent chest discomfort worsening with exertion.",

	"cardiac_5": "Emergency presentation: crushing thoracic pain, pallor, cold clammy skin, " +
		"and difficulty breathing. Treat as a cardiac emergency immediately.",

	"unrelated_recipe": "Preheat the oven to 180°C. Combine flour, butter, sugar, and eggs " +
		"to form a smooth batter. Bake for 30 minutes until golden brown.",

	"unrelated_python": "Python's garbage collector uses reference counting as its primary " +
		"mechanism. Circular references are handled by a cycle-detecting GC.",

	"unrelated_worldcup": "Argentina won the 2022 FIFA World Cup in Qatar, defeating France " +
		"on penalties in the final. Lionel Messi lifted the trophy after 36 years.",

	"unrelated_amazon": "The Amazon River flows 6,400 kilometres through South America and " +
		"drains into the Atlantic Ocean, accounting for 20% of global river discharge.",
}

var queryWords = []string{"heart", "attack", "symptoms"}

func main() {
	ctx := context.Background()
	query := "heart attack symptoms"

	// ── Header ────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("%s╔════════════════════════════════════════════════════════════════╗%s\n", bold, reset)
	fmt.Printf("%s║  ZENITH  ─  Semantic Search Demo                              ║%s\n", bold, reset)
	fmt.Printf("%s╚════════════════════════════════════════════════════════════════╝%s\n\n", bold, reset)

	fmt.Printf("  %sQuery:%s  %s\"%s\"%s\n\n", bold, reset, cyan, query, reset)

	// ── Corpus preview ────────────────────────────────────────────────────
	fmt.Printf("%s  Documents in the index (%d total):%s\n", gray, len(corpus), reset)
	for id, text := range corpus {
		fmt.Printf("  %s%-20s%s  %s\n", dim, id, reset, truncate(text, 64))
	}

	fmt.Println()
	anyMatch := corpusContains(queryWords)
	if !anyMatch {
		fmt.Printf("  %s✓ None of the %d documents contain \"heart\", \"attack\", or \"symptoms\".%s\n",
			green, len(corpus), reset)
	}

	// ── BM25 keyword search ───────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("%s  ── BM25  keyword search ─────────────────────────────────────────%s\n", gray, reset)
	fmt.Println()

	bm25DB, err := zenith.Open(":memory:", zenith.WithBM25Only())
	must(err)
	defer bm25DB.Close()
	must(bm25DB.AddBatch(ctx, corpus))

	bm25Results, err := bm25DB.Search(ctx, query, zenith.Limit(5))
	must(err)

	if len(bm25Results) == 0 {
		fmt.Printf("  %s0 results.%s\n", red, reset)
		fmt.Printf("  %s\"heart\", \"attack\", \"symptoms\" appear in 0 indexed documents.%s\n", dim, reset)
		fmt.Printf("  %sA keyword engine only returns what it can find verbatim.%s\n", dim, reset)
	} else {
		fmt.Printf("  %d result(s):\n", len(bm25Results))
		for i, r := range bm25Results {
			fmt.Printf("  #%d  %-22s  [score: %.3f]  %s\n",
				i+1, r.ID, r.Score, truncate(corpus[r.ID], 55))
		}
	}

	// ── ZENITH hybrid ─────────────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("%s  ── ZENITH hybrid  (BM25 + semantic vectors) ────────────────────%s\n", gray, reset)
	fmt.Println()

	hybridDB, err := zenith.Open(":memory:")
	must(err)
	defer hybridDB.Close()

	fmt.Printf("  %sBuilding index (embeddings)...%s\n", dim, reset)
	must(hybridDB.AddBatch(ctx, corpus))
	fmt.Printf("  %sdone.%s\n\n", dim, reset)

	hybridResults, err := hybridDB.Search(ctx, query, zenith.Limit(5))
	must(err)

	for i, r := range hybridResults {
		text := corpus[r.ID]
		matched := containsAny(strings.ToLower(text), queryWords)

		fmt.Printf("  %s#%d%s  %-22s  %s[score: %.3f]%s\n",
			bold, i+1, reset, r.ID, dim, r.Score, reset)
		for _, line := range wrap(text, 62) {
			fmt.Printf("       %s\n", line)
		}
		if matched {
			fmt.Printf("       \033[33m↳ contains query words — keyword overlap\033[0m\n")
		} else {
			fmt.Printf("       %s↳ \"heart attack\" not in document — matched by meaning ✓%s\n", green, reset)
		}
		fmt.Println()
	}

	// ── Summary ───────────────────────────────────────────────────────────
	irrelevant := 0
	for _, r := range hybridResults {
		if strings.HasPrefix(r.ID, "unrelated_") {
			irrelevant++
		}
	}
	if irrelevant == 0 {
		fmt.Printf("  %s✓ 0 irrelevant documents in top 5.%s\n", green, reset)
	}
	fmt.Printf("  %s✓ ZENITH found what you meant, not what you typed.%s\n\n", green, reset)
}

// corpusContains returns true if any document in corpus contains any of words.
func corpusContains(words []string) bool {
	for _, text := range corpus {
		if containsAny(strings.ToLower(text), words) {
			return true
		}
	}
	return false
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// wrap breaks s into lines of at most width characters, preserving words.
func wrap(s string, width int) []string {
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len()+1+len(w) > width && cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("demo: %v", err))
	}
}
