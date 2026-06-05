package report

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations.sql
var schemaSQL string

// Store is the PostgreSQL backup-history store.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool to dsn.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// Migrate applies the idempotent schema (CREATE TABLE IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schemaSQL)
	return err
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }
