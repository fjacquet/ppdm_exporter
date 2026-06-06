package render

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/reporttest"
)

// storeFor spins up a migrated store with one compliant + one copies-failing asset for tenant
// acme, seeded through the store's exported Upsert* methods (no raw SQL / pool access needed).
func storeFor(t *testing.T) *report.Store {
	t.Helper()
	st := reporttest.NewStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertSLATargets(ctx, []report.SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	if err := st.UpsertAssets(ctx, "acme", "s1", []report.Asset{
		{ID: "ok", Name: "ok", Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED", LastAvailableCopyTime: recent},
		{ID: "cop", Name: "cop", Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED", LastAvailableCopyTime: recent},
	}, now); err != nil {
		t.Fatal(err)
	}
	ct := now.Add(-time.Hour).Format(time.RFC3339)
	rt := now.Add(30*24*time.Hour + time.Hour).Format(time.RFC3339) // ~31d retention span
	cp := func(id, asset string) report.Copy {
		return report.Copy{ID: id, AssetID: asset, CreateTime: ct, RetentionTime: rt}
	}
	// asset "ok" gets 2 copies (meets min-copies 2) -> compliant; "cop" gets 1 -> copies fail.
	if err := st.UpsertCopies(ctx, "acme", "s1",
		[]report.Copy{cp("o1", "ok"), cp("o2", "ok"), cp("p1", "cop")}, now); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBuild(t *testing.T) {
	st := storeFor(t)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	d, err := Build(context.Background(), st, "acme", "Acme Co", now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d.Tenant != "acme" || d.BrandName != "Acme Co" || !d.GeneratedAt.Equal(now) {
		t.Fatalf("header = %+v", d)
	}
	if d.Summary.TotalAssets != 2 || d.Summary.CompliantAssets != 1 {
		t.Errorf("summary = %+v", d.Summary)
	}
	if len(d.Compliance) != 2 || len(d.Rule321) != 2 {
		t.Errorf("rows: compliance=%d rule321=%d", len(d.Compliance), len(d.Rule321))
	}
}

func TestBuildNoAssets(t *testing.T) {
	st := reporttest.NewStore(t)
	if _, err := Build(context.Background(), st, "ghost", "B", time.Unix(0, 0)); !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}
