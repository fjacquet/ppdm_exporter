package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/reporttest"
)

type fakeDeliverer struct {
	calls    int
	failNext bool
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ string, _ []string, _ string, _, _ []byte) error {
	f.calls++
	if f.failNext {
		return errors.New("smtp boom")
	}
	return nil
}

// seedTenant gives "acme" one asset + a copy + a default target so render.Build succeeds.
func seedTenant(t *testing.T, st *report.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertSLATargets(ctx, []report.SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAssets(ctx, "acme", "s1", []report.Asset{
		{ID: "v1", Name: "vm", Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED",
			LastAvailableCopyTime: now.Add(-time.Hour).Format(time.RFC3339)},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCopies(ctx, "acme", "s1", []report.Copy{
		{ID: "c1", AssetID: "v1", CreateTime: now.Add(-time.Hour).Format(time.RFC3339),
			RetentionTime: now.Add(30 * 24 * time.Hour).Format(time.RFC3339)},
	}, now); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerSendsOncePerPeriod(t *testing.T) {
	st := reporttest.NewStore(t)
	seedTenant(t, st)
	fd := &fakeDeliverer{}
	sc := New(st, fd, []config.Schedule{
		{Tenant: "acme", Cadence: "daily", Hour: 0, Recipients: []string{"ops@acme.com"}},
	}, "Acme Co")
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC) // past hour 0 -> due

	sc.runDue(ctx, now)
	if fd.calls != 1 {
		t.Fatalf("first tick calls = %d, want 1", fd.calls)
	}
	sc.runDue(ctx, now) // same period -> no resend
	if fd.calls != 1 {
		t.Fatalf("second tick calls = %d, want 1 (deduped)", fd.calls)
	}
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-06-06"); !ok {
		t.Error("expected recorded delivery")
	}
}

func TestSchedulerRetriesOnFailure(t *testing.T) {
	st := reporttest.NewStore(t)
	seedTenant(t, st)
	fd := &fakeDeliverer{failNext: true}
	sc := New(st, fd, []config.Schedule{
		{Tenant: "acme", Cadence: "daily", Hour: 0, Recipients: []string{"ops@acme.com"}},
	}, "Acme Co")
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)

	sc.runDue(ctx, now) // fails -> ok=false, no dedupe
	if fd.calls != 1 {
		t.Fatalf("calls = %d, want 1", fd.calls)
	}
	// A failed row must NOT count as a successful delivery.
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-06-06"); ok {
		t.Error("failed delivery should not satisfy DeliveryExists")
	}
	fd.failNext = false
	sc.runDue(ctx, now) // retries because the prior attempt failed
	if fd.calls != 2 {
		t.Fatalf("calls after retry = %d, want 2", fd.calls)
	}
	// After success the period is locked.
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-06-06"); !ok {
		t.Error("successful retry should satisfy DeliveryExists")
	}
}

func TestSchedulerSkipsTenantWithNoData(t *testing.T) {
	st := reporttest.NewStore(t)
	fd := &fakeDeliverer{}
	sc := New(st, fd, []config.Schedule{
		{Tenant: "ghost", Cadence: "daily", Hour: 0, Recipients: []string{"x@y.z"}},
	}, "B")
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	sc.runDue(ctx, now)
	if fd.calls != 0 {
		t.Errorf("no-data tenant should not deliver; calls = %d", fd.calls)
	}
	// A no-data skip records ok=false, so DeliveryExists (which checks ok=true) must be false.
	if ok, _ := st.DeliveryExists(ctx, "ghost", PeriodKey(now, sc.schedules[0])); ok {
		t.Error("no-data skip must not satisfy DeliveryExists")
	}
}
