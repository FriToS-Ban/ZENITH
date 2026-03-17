package ranking

type ScoredResult struct {
	ID    string
	Score float64
}

type Candidate struct {
	ID    uint32
	Score float64
}

type Scorer interface {
    Score(keywordIDs []uint32, keywordScores map[uint32]float64, vectorIDs []uint32, vectorScores map[uint32]float64, idMapping map[uint32]string) []ScoredResult
}
