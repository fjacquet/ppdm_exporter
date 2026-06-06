package report

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestStore spins up a throwaway Postgres and returns a migrated Store.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres testcontainers in -short mode")
	}
	ctx := context.Background()
	// The postgres image opens 5432 during init and then restarts, so waiting on the port
	// alone races init (connections in that window are reset). Wait for the "ready to accept
	// connections" log to appear a SECOND time — the real server, after init — to avoid flakes.
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("backup_report"),
		tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestMigrateCreatesTables(t *testing.T) {
	st := newTestStore(t)
	var n int
	err := st.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_name = ANY($1)`,
		[]string{"backup_jobs", "copies", "assets", "protection_policies", "capture_runs"},
	).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("created %d/5 tables", n)
	}
}
