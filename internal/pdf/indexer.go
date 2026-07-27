package pdf

import (
	"context"
	"fmt"
	"strings"

	lpdf "github.com/ledongthuc/pdf"
	"github.com/shramanb113/ZENITH/internal/activitylog"
	"github.com/shramanb113/ZENITH/internal/index"
)

const (
	chunkWords   = 300
	chunkOverlap = 50
)

// PDFIndexer extracts text from PDF files and indexes them into the engine.
// The engine's wired embedder handles vector computation automatically.
type PDFIndexer struct {
	engine *index.Engine
	logger *activitylog.Logger
}

// NewIndexer creates a PDFIndexer. An optional logger may be supplied.
func NewIndexer(e *index.Engine, logger ...*activitylog.Logger) *PDFIndexer {
	var l *activitylog.Logger
	if len(logger) > 0 && logger[0] != nil {
		l = logger[0]
	} else {
		l = activitylog.Noop()
	}
	return &PDFIndexer{engine: e, logger: l}
}

// Index extracts text from filePath and stores it in the engine.
// Returns the number of chunks indexed.
func (p *PDFIndexer) Index(ctx context.Context, docID, filePath string) (int, error) {
	f, r, err := lpdf.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("pdf: open %s: %w", filePath, err)
	}
	defer f.Close()

	var docs []index.BatchDoc
	for pageNum := 1; pageNum <= r.NumPage(); pageNum++ {
		page := r.Page(pageNum)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		for chunkIdx, chunk := range splitChunks(text) {
			docs = append(docs, index.BatchDoc{
				ID:   fmt.Sprintf("%s||p%d||c%d||text||0.00,0.00,0.00,0.00", docID, pageNum, chunkIdx),
				Text: chunk,
			})
		}
	}

	if len(docs) == 0 {
		return 0, nil
	}
	if err := p.engine.AddBatch(ctx, docs); err != nil {
		return 0, fmt.Errorf("pdf: index %s: %w", docID, err)
	}
	p.logger.Log("PDF", fmt.Sprintf("%s → %d chunks indexed", docID, len(docs)))
	return len(docs), nil
}

func splitChunks(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var chunks []string
	for i := 0; i < len(words); {
		end := i + chunkWords
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
		if end == len(words) {
			break
		}
		i += chunkWords - chunkOverlap
	}
	return chunks
}
