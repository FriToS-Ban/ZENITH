package ranking

type ScoredResult struct {
	ID    string
	Score float64
}

type Candidate struct {
	ID    uint64
	Score float64
}

type Scorer interface {
    Score(keywordIDs []uint64, keywordScores map[uint64]float64, vectorIDs []uint64, vectorScores map[uint64]float64, idMapping map[uint64]string) []ScoredResult
}
