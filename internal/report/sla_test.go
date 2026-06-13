package report

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func goldVMConfig() config.Compliance {
	return config.Compliance{
		Grace:    4 * time.Hour,
		Defaults: config.ComplianceTarget{RPOHours: 24, RetentionDays: 30, MinCopies: 2},
		Overrides: []config.ComplianceOverride{
			{
				Tenant:           "acme",
				AssetType:        "VMWARE_VIRTUAL_MACHINE",
				ComplianceTarget: config.ComplianceTarget{RPOHours: 12, MinCopies: 3},
			},
		},
	}
}

// target reads one sla_targets row; found=false when absent.
func target(t *testing.T, st *Store, tenant, assetType, policy string) (rpo int64, ret, minc int, grace int64, source string, found bool) {
	t.Helper()
	err := st.pool.QueryRow(context.Background(),
		`SELECT rpo_seconds, retention_days, min_copies, grace_seconds, source FROM sla_targets
		 WHERE tenant=$1 AND asset_type=$2 AND policy_name=$3`, tenant, assetType, policy).
		Scan(&rpo, &ret, &minc, &grace, &source)
	if err != nil {
		return 0, 0, 0, 0, "", false
	}
	return rpo, ret, minc, grace, source, true
}

func TestResolveTargets(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	var obj any
	if err := json.Unmarshal([]byte(
		`[{"type":"BACKUP","schedule":{"interval":"PT24H"},"retention":{"interval":"P30D"}}]`), &obj); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPolicies(ctx, "acme", "s1",
		[]Policy{{ID: "p1", Name: "Gold-VM", Objectives: obj}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	capt := NewCapturer(st, "v-test", config.Retention{DefaultDays: 400}, config.Compliance{})
	if err := capt.ResolveTargets(ctx, "acme", goldVMConfig()); err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}

	// Default row.
	rpo, ret, minc, grace, src, ok := target(t, st, "acme", "", "")
	if !ok || rpo != 86400 || ret != 30 || minc != 2 || grace != 14400 || src != "default" {
		t.Errorf("default = rpo=%d ret=%d min=%d grace=%d src=%s ok=%v", rpo, ret, minc, grace, src, ok)
	}
	// Policy-derived row (PT24H -> 86400s, P30D -> 30d).
	rpo, ret, _, _, src, ok = target(t, st, "acme", "", "Gold-VM")
	if !ok || rpo != 86400 || ret != 30 || src != "policy" {
		t.Errorf("policy = rpo=%d ret=%d src=%s ok=%v", rpo, ret, src, ok)
	}
	// Override row: rpo 12h, min 3; retention falls back to default (30d).
	rpo, ret, minc, _, src, ok = target(t, st, "acme", "VMWARE_VIRTUAL_MACHINE", "")
	if !ok || rpo != 43200 || ret != 30 || minc != 3 || src != "override" {
		t.Errorf("override = rpo=%d ret=%d min=%d src=%s ok=%v", rpo, ret, minc, src, ok)
	}
}

func TestCaptureServerResolvesTargets(t *testing.T) {
	st := newTestStore(t)
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", `{"page":{"totalPages":1},"content":[]}`)
	m.SetJSONPrefix("/api/v2/latest-copies", `{"page":{"totalPages":1},"content":[]}`)
	m.SetJSONPrefix("/api/v2/assets", `{"page":{"totalPages":1},"content":[
		{"id":"v1","name":"vm","type":"VMWARE_VIRTUAL_MACHINE","protectionPolicy":{"name":"Gold-VM"}}]}`)
	m.SetJSONPrefix("/api/v3/protection-policies", `{"page":{"totalPages":1},"content":[
		{"id":"p1","name":"Gold-VM","objectives":[{"type":"BACKUP","schedule":{"interval":"PT24H"},"retention":{"interval":"P30D"}}]}]}`)

	capt := NewCapturer(st, "v-test", config.Retention{DefaultDays: 400}, goldVMConfig())
	if err := capt.CaptureServer(context.Background(), "acme", m); err != nil {
		t.Fatalf("CaptureServer: %v", err)
	}

	// Capture must have resolved targets as a post-step: the Gold-VM policy row exists.
	rpo, ret, _, _, src, ok := target(t, st, "acme", "", "Gold-VM")
	if !ok || rpo != 86400 || ret != 30 || src != "policy" {
		t.Errorf("policy target after capture = rpo=%d ret=%d src=%s ok=%v", rpo, ret, src, ok)
	}
	if _, _, _, _, _, ok := target(t, st, "acme", "", ""); !ok {
		t.Error("default target row missing after capture")
	}
}

// verdict reads one compliance-view row.
func verdict(t *testing.T, st *Store, assetID string) (compliant bool, reasons string) {
	t.Helper()
	if err := st.pool.QueryRow(context.Background(),
		`SELECT compliant, reasons FROM compliance WHERE asset_id=$1`, assetID).
		Scan(&compliant, &reasons); err != nil {
		t.Fatalf("verdict %s: %v", assetID, err)
	}
	return compliant, reasons
}

func TestComplianceView(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}

	// Targets: a per-tenant default and a Gold-VM policy target (both rpo 24h, ret 30d, min 2).
	if err := st.UpsertSLATargets(ctx, []SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
		{Tenant: "acme", PolicyName: "Gold-VM", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "policy"},
	}); err != nil {
		t.Fatal(err)
	}

	// Assets (server s1). last_available_copy_time is DB-clock relative to avoid skew.
	exec(`INSERT INTO assets (id,tenant,server,name,type,protection_status,last_available_copy_time,policy_name,updated_at,captured_at) VALUES
		('a_ok','acme','s1','ok','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','Gold-VM', now(), now()),
		('a_rpo','acme','s1','rpo','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '3 days','Gold-VM', now(), now()),
		('a_ret','acme','s1','ret','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','Gold-VM', now(), now()),
		('a_cop','acme','s1','cop','VMWARE_VIRTUAL_MACHINE','PROTECTED', now()-interval '1 hour','Gold-VM', now(), now()),
		('a_def','acme','s1','def','FILE_SYSTEM','PROTECTED', now()-interval '1 hour','Unknown', now(), now())`)

	// a_ok: a recent SUCCESS job (rpo via job path).
	exec(`INSERT INTO backup_jobs (id,tenant,server,result_status,asset_id,created_at,captured_at) VALUES
		('j_ok','acme','s1','SUCCESS','a_ok', now()-interval '1 hour', now())`)

	// Copies: newest (by create_time) drives retention. span = retention_time - create_time.
	exec(`INSERT INTO copies (id,tenant,server,asset_id,create_time,retention_time,captured_at) VALUES
		('c_ok1','acme','s1','a_ok', now()-interval '2 hours', now()-interval '2 hours'+interval '31 days', now()),
		('c_ok2','acme','s1','a_ok', now()-interval '1 hour',  now()-interval '1 hour'+interval '31 days', now()),
		('c_rpo1','acme','s1','a_rpo', now()-interval '2 hours', now()-interval '2 hours'+interval '31 days', now()),
		('c_rpo2','acme','s1','a_rpo', now()-interval '1 hour',  now()-interval '1 hour'+interval '31 days', now()),
		('c_ret1','acme','s1','a_ret', now()-interval '2 hours', now()-interval '2 hours'+interval '31 days', now()),
		('c_ret2','acme','s1','a_ret', now()-interval '1 hour',  now()-interval '1 hour'+interval '10 days', now()),
		('c_cop1','acme','s1','a_cop', now()-interval '1 hour',  now()-interval '1 hour'+interval '31 days', now()),
		('c_def1','acme','s1','a_def', now()-interval '2 hours', now()-interval '2 hours'+interval '31 days', now()),
		('c_def2','acme','s1','a_def', now()-interval '1 hour',  now()-interval '1 hour'+interval '31 days', now())`)

	cases := []struct {
		asset         string
		wantCompliant bool
		wantReasons   string
	}{
		{"a_ok", true, ""},
		{"a_rpo", false, "rpo"},
		{"a_ret", false, "retention"},
		{"a_cop", false, "copies"},
		{"a_def", true, ""}, // no Gold-VM match (policy 'Unknown') -> falls to tenant default
	}
	for _, c := range cases {
		gotC, gotR := verdict(t, st, c.asset)
		if gotC != c.wantCompliant || gotR != c.wantReasons {
			t.Errorf("%s: compliant=%v reasons=%q, want compliant=%v reasons=%q",
				c.asset, gotC, gotR, c.wantCompliant, c.wantReasons)
		}
	}
}
