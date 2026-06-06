// Package reporttest spins up a migrated report.Store backed by a real (throwaway) Postgres via
// testcontainers, for use by tests in sibling packages (e.g. report/render).
package reporttest

import (
	"context"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewStore returns a migrated Store on a throwaway Postgres; skipped under -short. Waits on the
// second "ready to accept connections" log to avoid the init-restart connection-reset flake.
func NewStore(t *testing.T) *report.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres testcontainers in -short mode")
	}
	ctx := context.Background()
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
	st, err := report.New(ctx, dsn)
	if err != nil {
		t.Fatalf("New store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}
