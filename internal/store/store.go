// Package store saves the agent's memory in Postgres.
package store

import (
	"context"
	_ "embed" // the blank "_" import enables //go:embed below
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// go:embed copies schema.sql INTO the compiled program at build time,
// so the binary doesn't need the .sql file next to it when deployed.
//
//go:embed schema.sql
var schema string

// Store wraps a connection pool. A pool keeps a few database connections
// open and reuses them — opening a new one per query would be slow.
type Store struct {
	pool *pgxpool.Pool
}

// Application is one row of the applications table.
type Application struct {
	ThreadID   string
	CompanyKey string
	To         string
	Subject    string
	AppliedAt  time.Time
	Status     string
	Reason     string
}

// Open connects to Postgres and makes sure the tables exist.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	// pgxpool connects lazily; Ping forces a real connection so we fail fast
	// with a clear error if Docker isn't running.
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres (is `docker compose up -d` running?): %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("create tables: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the database connections.
func (s *Store) Close() { s.pool.Close() }

// SaveApplication inserts the row, or updates it if we've seen this thread before.
// This "insert or update" is called an UPSERT.
//
// $1, $2... are *parameters*: pgx sends the values separately from the SQL text.
// NEVER build SQL by gluing strings together (fmt.Sprintf) — a subject line like
// `'; DROP TABLE applications; --` would become code. That attack is "SQL injection".
func (s *Store) SaveApplication(ctx context.Context, a Application) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO applications (thread_id, company_key, to_address, subject, applied_at, status, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (thread_id) DO UPDATE SET
			status     = EXCLUDED.status,   -- EXCLUDED = the row we tried to insert
			reason     = EXCLUDED.reason,
			checked_at = now()`,
		a.ThreadID, a.CompanyKey, a.To, a.Subject, a.AppliedAt, a.Status, a.Reason)
	if err != nil {
		return fmt.Errorf("save application %s: %w", a.ThreadID, err)
	}
	return nil
}

// LastRun returns when the agent last finished a run. ok=false on the very first run.
func (s *Store) LastRun(ctx context.Context) (t time.Time, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT started_at FROM runs ORDER BY started_at DESC LIMIT 1`).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil // no runs yet — not an error
	}
	return t, err == nil, err
}

// RecordRun remembers that a run happened.
func (s *Store) RecordRun(ctx context.Context, startedAt time.Time, checked, due int) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO runs (started_at, checked, due) VALUES ($1, $2, $3)`,
		startedAt, checked, due)
	return err
}

// MarkDrafted records that a follow-up draft now exists for this application.
func (s *Store) MarkDrafted(ctx context.Context, threadID, draftID, company, role string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE applications
		SET status = 'drafted', draft_id = $2, company = $3, role = $4, drafted_at = now()
		WHERE thread_id = $1`,
		threadID, draftID, company, role)
	return err
}
