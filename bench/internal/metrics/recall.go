package metrics

// RecallAt10 computes Recall@10 given a map of query results and ground truth.
//
//   results  — map[queryID][]docID (ranked, up to 10)
//   qrels    — map[queryID]docID   (single relevant document per query)
//   subset   — set of docIDs present in the indexed corpus
//
// Only queries whose relevant document exists in the subset contribute to the
// denominator. Queries with no relevant passage in the subset are skipped so
// the engine is not penalised for passages it never had a chance to index.
func RecallAt10(
	results map[string][]string,
	qrels map[string]string,
	subset map[string]struct{},
) float64 {
	hits, total := 0, 0
	for qid, relevantID := range qrels {
		if _, inSubset := subset[relevantID]; !inSubset {
			continue
		}
		total++
		for _, id := range results[qid] {
			if id == relevantID {
				hits++
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
