package report

import (
	"context"
	"testing"
	"time"
)

func TestWatermarkAndPrune(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	// One old job (beyond retention) and one recent.
	old := Job{ID: "old", Category: "PROTECT", CreatedAt: "2024-01-01T00:00:00Z"}
	recent := Job{ID: "new", Category: "PROTECT", CreatedAt: "2026-06-05T01:00:00Z"}
	if err := st.UpsertJobs(ctx, "acme", "ppdm01", []Job{old, recent}, now); err != nil {
		t.Fatal(err)
	}

	wm, err := st.JobWatermark(ctx, "ppdm01")
	if err != nil {
		t.Fatal(err)
	}
	if wm.UTC() != time.Date(2026, 6, 5, 1, 0, 0, 0, time.UTC) {
		t.Fatalf("watermark = %v, want 2026-06-05T01:00", wm)
	}

	if err := st.Prune(ctx, 400, nil); err != nil { // ~13 months, global
		t.Fatal(err)
	}
	var count int
	_ = st.pool.QueryRow(ctx, `SELECT count(*) FROM backup_jobs`).Scan(&count)
	if count != 1 {
		t.Fatalf("after prune rows = %d, want 1 (old removed)", count)
	}
}

func TestRecordRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, err := st.StartRun(ctx, "ppdm01", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, id, true, "", map[string]int{"jobs": 3}); err != nil {
		t.Fatal(err)
	}
	var ok bool
	_ = st.pool.QueryRow(ctx, `SELECT ok FROM capture_runs WHERE id=$1`, id).Scan(&ok)
	if !ok {
		t.Fatal("run not marked ok")
	}
}
