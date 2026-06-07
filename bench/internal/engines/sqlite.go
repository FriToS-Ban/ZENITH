package engines

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteEngine runs queries against an in-memory SQLite FTS5 virtual table.
// Uses mattn/go-sqlite3 (CGo) — the same CGo bar as ZENITH hybrid ONNX mode.
type SQLiteEngine struct {
	db *sql.DB
}

func NewSQLiteEngine() (*SQLiteEngine, error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}

	setup := []string{
		`CREATE TABLE docs (id TEXT PRIMARY KEY, body TEXT)`,
		`CREATE VIRTUAL TABLE docs_fts USING fts5(id UNINDEXED, body, content=docs, content_rowid=rowid)`,
	}
	for _, stmt := range setup {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite: setup: %w", err)
		}
	}
	return &SQLiteEngine{db: db}, nil
}

func (e *SQLiteEngine) Name() string { return "SQLite FTS5" }

func (e *SQLiteEngine) Index(ctx context.Context, id, text string) error {
	_, err := e.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO docs(id, body) VALUES (?, ?)`, id, text)
	if err != nil {
		return fmt.Errorf("sqlite: insert: %w", err)
	}
	_, err = e.db.ExecContext(ctx,
		`INSERT INTO docs_fts(id, body) VALUES (?, ?)`, id, text)
	return err
}

func (e *SQLiteEngine) IndexBatch(ctx context.Context, docs map[string]string) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	insDoc, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO docs(id, body) VALUES (?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	insFTS, err := tx.PrepareContext(ctx,
		`INSERT INTO docs_fts(id, body) VALUES (?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}

	for id, text := range docs {
		if _, err := insDoc.ExecContext(ctx, id, text); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlite: insert doc: %w", err)
		}
		if _, err := insFTS.ExecContext(ctx, id, text); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlite: insert fts: %w", err)
		}
	}
	return tx.Commit()
}

// sanitizeFTS5 strips FTS5 syntax characters and builds an OR query so that
// FTS5 behaves like BM25 OR-semantics (any term can match, ranked by relevance)
// rather than the default AND-semantics (all terms must appear).
func sanitizeFTS5(q string) string {
	var b strings.Builder
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	terms := strings.Fields(b.String())
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

func (e *SQLiteEngine) Search(ctx context.Context, query string, topK int) ([]string, error) {
	clean := sanitizeFTS5(query)
	if clean == "" {
		return nil, nil
	}
	rows, err := e.db.QueryContext(ctx,
		`SELECT id FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT ?`,
		clean, topK)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *SQLiteEngine) Close() error { return e.db.Close() }
