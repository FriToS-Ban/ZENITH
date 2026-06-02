package pdf

import (
	"context"
	"fmt"

	"github.com/shramanb113/ZENITH/internal/activitylog"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/nerve"
)

// PDFIndexer extracts and indexes a PDF document through the Nerve sidecar.
// Each chunk ID encodes page, position, source type, and bounding box so
// callers can pinpoint the exact location of any search result.
type PDFIndexer struct {
	nerve  *nerve.NerveClient
	engine *index.Engine
	logger *activitylog.Logger
}

// NewIndexer creates a PDFIndexer wired to the given sidecar client and engine.
// An optional logger may be supplied; if omitted a no-op logger is used.
func NewIndexer(n *nerve.NerveClient, e *index.Engine, logger ...*activitylog.Logger) *PDFIndexer {
	var l *activitylog.Logger
	if len(logger) > 0 && logger[0] != nil {
		l = logger[0]
	} else {
		l = activitylog.Noop()
	}
	return &PDFIndexer{nerve: n, engine: e, logger: l}
}

// Index extracts, embeds, and stores all chunks from the PDF at filePath.
// Returns the number of chunks indexed. Uses AddBatch to rebuild the FST
// exactly once after all chunks are written, avoiding the O(vocab·log vocab)
// rebuild cost that would otherwise fire after every individual chunk.
func (p *PDFIndexer) Index(ctx context.Context, docID, filePath string) (int, error) {
	chunks, err := p.nerve.ExtractPDF(ctx, docID, filePath)
	if err != nil {
		return 0, fmt.Errorf("pdf: extract %s: %w", filePath, err)
	}

	docs := make([]index.BatchDoc, len(chunks))
	for i, c := range chunks {
		docs[i] = index.BatchDoc{
			ID: fmt.Sprintf("%s||p%d||c%d||%s||%.2f,%.2f,%.2f,%.2f",
				docID, c.Page, c.ChunkIndex, c.SourceType,
				c.BboxX, c.BboxY, c.BboxW, c.BboxH,
			),
			Text:   c.Text,
			Vector: c.Embedding,
		}
	}

	if err := p.engine.AddBatch(ctx, docs); err != nil {
		return 0, fmt.Errorf("pdf: index chunks %s: %w", docID, err)
	}

	p.logger.Log("PDF", fmt.Sprintf("%s → %d chunks indexed", docID, len(chunks)))
	return len(chunks), nil
}
