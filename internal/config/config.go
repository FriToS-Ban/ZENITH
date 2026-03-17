package config

import "time"

type Config struct {
	// Storage
	WALDir          string
	DataDir         string
	MemTableMaxSize int64

	// Search
	MaxResults      int
	FuzzyMaxDist    int
	BloomFPRate     float64
	RRFConstant     float64
	PhoneticWeight  float64
	VectorWeight    float64
	NeuralWeight    float64


	// Performance
	EmbeddingWorkers int
	CompactionInterv time.Duration
	WALSyncInterval  time.Duration

	// Nerve
	NerveURL     string
	NerveTimeout time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		WALDir:           "./data/wal",
		DataDir:          "./data/sst",
		MemTableMaxSize:  67108864, // 64MB
		MaxResults:       10,
		FuzzyMaxDist:     2,
		BloomFPRate:      0.01,
		RRFConstant:      60.0,
		EmbeddingWorkers: 4,
		CompactionInterv: 30 * time.Second,
		WALSyncInterval:  100 * time.Millisecond,
		NerveURL:         "http://localhost:8000",
		NerveTimeout:     5 * time.Second,
		PhoneticWeight:   0.3,
		VectorWeight:     0.7,
		NeuralWeight:     1.0,
	}
}
