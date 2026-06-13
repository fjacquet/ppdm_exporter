package report

import (
	"context"
	"testing"
	"time"
)

func TestUpsertJobsIdempotent(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	jobs := []Job{{ID: "j1", Category: "PROTECT", State: "COMPLETED",
		CreateTime: "2026-06-05T01:00:00Z", EndTime: "2026-06-05T01:04:12Z"}}
	jobs[0].Result.Status = "SUCCESS"
	jobs[0].Result.BytesTransferred = 1048576
	jobs[0].Asset.Name = "vm-app01"

	for i := 0; i < 2; i++ { // upsert twice -> still one row
		if err := st.UpsertJobs(ctx, "acme", "ppdm01", jobs, now); err != nil {
			t.Fatalf("UpsertJobs: %v", err)
		}
	}
	var count int
	var status, asset string
	var bytes int64
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM backup_jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1 (idempotent)", count)
	}
	_ = st.pool.QueryRow(ctx, `SELECT result_status, asset_name, bytes_transferred FROM backup_jobs WHERE id='j1'`).
		Scan(&status, &asset, &bytes)
	if status != "SUCCESS" || asset != "vm-app01" || bytes != 1048576 {
		t.Fatalf("row = %s/%s/%d", status, asset, bytes)
	}
	// Regression (ADR-0010): created_at and started_at both derive from createTime, completed_at from endTime.
	var createdAt, startedAt, completedAt time.Time
	if err := st.pool.QueryRow(ctx, `SELECT created_at, started_at, completed_at FROM backup_jobs WHERE id='j1'`).
		Scan(&createdAt, &startedAt, &completedAt); err != nil {
		t.Fatal(err)
	}
	wantCreate, _ := time.Parse(time.RFC3339, "2026-06-05T01:00:00Z")
	wantEnd, _ := time.Parse(time.RFC3339, "2026-06-05T01:04:12Z")
	if !createdAt.Equal(wantCreate) || !startedAt.Equal(wantCreate) {
		t.Fatalf("created_at=%s started_at=%s, want both %s", createdAt, startedAt, wantCreate)
	}
	if !completedAt.Equal(wantEnd) {
		t.Fatalf("completed_at=%s, want %s", completedAt, wantEnd)
	}
}

func TestUpsertCopiesAndAssets(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertCopies(ctx, "acme", "ppdm01", []Copy{{ID: "c1", AssetID: "a1",
		CopyType: "FULL", CreateTime: "2026-06-05T01:04:00Z", RetentionTime: "2026-07-05T01:04:00Z",
		RetentionLock: true, Location: "ddve-01", Size: 1048576}}, now); err != nil {
		t.Fatalf("UpsertCopies: %v", err)
	}
	if err := st.UpsertAssets(ctx, "acme", "ppdm01", []Asset{{ID: "a1", Name: "vm-app01",
		Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED",
		LastAvailableCopyTime: "2026-06-05T01:04:00Z"}}, now); err != nil {
		t.Fatalf("UpsertAssets: %v", err)
	}
	var lock bool
	_ = st.pool.QueryRow(ctx, `SELECT retention_lock FROM copies WHERE id='c1'`).Scan(&lock)
	if !lock {
		t.Fatal("retention_lock not stored")
	}
}

func TestUpsertPolicies(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.UpsertPolicies(ctx, "acme", "ppdm01", []Policy{{ID: "p1", Name: "Gold-VM",
		Objectives: []map[string]any{{"type": "BACKUP"}}}}, time.Now()); err != nil {
		t.Fatalf("UpsertPolicies: %v", err)
	}
	var name string
	_ = st.pool.QueryRow(ctx, `SELECT name FROM protection_policies WHERE id='p1'`).Scan(&name)
	if name != "Gold-VM" {
		t.Fatalf("policy name = %q", name)
	}
}
