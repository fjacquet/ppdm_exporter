package report

import (
	"context"
	"testing"
)

func TestRule321Rows(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	// a_pass: 3 copies, 2 media, 2 locations, one immutable, no failed job -> full pass.
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,policy_name,updated_at,captured_at)
	      VALUES ('a_pass','acme','s1','full','VMWARE_VIRTUAL_MACHINE','PROTECTED','Gold-VM',now(),now())`)
	exec(`INSERT INTO copies (id,tenant,server,asset_id,storage_system_id,location,retention_lock,create_time,retention_time,captured_at) VALUES
	      ('p1','acme','s1','a_pass','dd-1','site-a',true , now(), now()+interval '31 days', now()),
	      ('p2','acme','s1','a_pass','dd-2','site-b',false, now(), now()+interval '31 days', now()),
	      ('p3','acme','s1','a_pass','dd-2','site-b',false, now(), now()+interval '31 days', now())`)
	// a_fail: 1 copy, 1 media, 1 location, not immutable, plus a FAILED job -> all-fail except none.
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,policy_name,updated_at,captured_at)
	      VALUES ('a_fail','acme','s1','thin','VMWARE_VIRTUAL_MACHINE','PROTECTED','Gold-VM',now(),now())`)
	exec(`INSERT INTO copies (id,tenant,server,asset_id,storage_system_id,location,retention_lock,create_time,retention_time,captured_at) VALUES
	      ('f1','acme','s1','a_fail','dd-1','site-a',false, now(), now()+interval '31 days', now())`)
	exec(`INSERT INTO backup_jobs (id,tenant,server,result_status,asset_id,created_at,captured_at)
	      VALUES ('jf','acme','s1','FAILED','a_fail', now(), now())`)

	rows, err := st.Rule321Rows(ctx, "acme")
	if err != nil {
		t.Fatalf("Rule321Rows: %v", err)
	}
	got := map[string]Rule321Row{}
	for _, r := range rows {
		got[r.AssetID] = r
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	p := got["a_pass"]
	if !(p.CopiesOk && p.MediaOk && p.OffsiteOk && p.ImmutableOk && p.ErrorsOk && p.RulePass) {
		t.Errorf("a_pass = %+v, want all true", p)
	}
	if p.CopiesCount != 3 || p.DistinctMedia != 2 || p.DistinctLocations != 2 {
		t.Errorf("a_pass counts = %+v", p)
	}
	f := got["a_fail"]
	if f.CopiesOk || f.MediaOk || f.OffsiteOk || f.ImmutableOk || f.ErrorsOk || f.RulePass {
		t.Errorf("a_fail = %+v, want all false", f)
	}
}

func TestComplianceRowsAndSummary(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	// Default target so the compliance view resolves; one compliant, one copies-failing asset.
	if err := st.UpsertSLATargets(ctx, []SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,last_available_copy_time,policy_name,updated_at,captured_at) VALUES
	      ('ok','acme','s1','ok','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','',now(),now()),
	      ('cop','acme','s1','cop','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','',now(),now())`)
	exec(`INSERT INTO copies (id,tenant,server,asset_id,create_time,retention_time,captured_at) VALUES
	      ('o1','acme','s1','ok', now()-interval '1 hour', now()+interval '31 days', now()),
	      ('o2','acme','s1','ok', now()-interval '2 hours', now()+interval '31 days', now()),
	      ('p1','acme','s1','cop', now()-interval '1 hour', now()+interval '31 days', now())`)

	cr, err := st.ComplianceRows(ctx, "acme")
	if err != nil {
		t.Fatalf("ComplianceRows: %v", err)
	}
	if len(cr) != 2 {
		t.Fatalf("compliance rows = %d, want 2", len(cr))
	}
	sum, err := st.ReportSummary(ctx, "acme")
	if err != nil {
		t.Fatalf("ReportSummary: %v", err)
	}
	if sum.TotalAssets != 2 || sum.CompliantAssets != 1 || sum.CopiesFailures != 1 {
		t.Errorf("summary = %+v, want total 2 / compliant 1 / copiesFail 1", sum)
	}
	if sum.BadgePass { // no asset has 3 copies / 2 media -> badge fails
		t.Errorf("badge should be false, got %+v", sum)
	}
}
