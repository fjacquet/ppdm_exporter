package ppdm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

const (
	rulesPath       = "/api/v3/rules"
	ruleMetricsPath = "/api/v3/protection-policy-rule-metrics"
)

var ruleNow = time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

func ruleMock(t *testing.T) *ppdmclient.Mock {
	t.Helper()
	m := ppdmclient.NewMock("ppdm01")
	for path, file := range map[string]string{
		rulesPath:       "testdata/rules.json",
		ruleMetricsPath: "testdata/protection-policy-rule-metrics.json",
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		m.SetJSONPrefix(path, string(b))
	}
	return m
}

func collectRules(t *testing.T, c ppdmclient.Client) []Sample {
	t.Helper()
	got, err := Rules{now: func() time.Time { return ruleNow }}.Collect(context.Background(), c)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return got
}

func TestRulesCollect(t *testing.T) {
	got := collectRules(t, ruleMock(t))
	seen := map[string]float64{}
	ages := 0
	for _, s := range got {
		switch s.Name {
		case "ppdm_rule_count":
			seen["count|"+s.LabelValue("status")] = s.Value
		case "ppdm_rule_last_run_age_seconds":
			ages++
			seen["age|"+s.LabelValue("rule")] = s.Value
		case "ppdm_rule_assigned_assets":
			seen["assigned|"+s.LabelValue("policy")+"|"+s.LabelValue("rule")] = s.Value
		}
	}
	want := map[string]float64{
		"count|ACTIVE": 2, "count|CREATING": 1,
		"age|vm-gold":                  86400,
		"assigned|Gold|vm-gold":        12,
		"assigned|Silver|fs-never-run": 0,
	}
	for k, v := range want {
		if g, ok := seen[k]; !ok || g != v {
			t.Errorf("%s = %v (present=%v), want %v", k, g, ok, v)
		}
	}
	if ages != 1 {
		t.Errorf("age samples = %d, want 1 (never-run rules have no age)", ages)
	}
}

func TestRulesNotFoundIsSilent(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	m.SetNotFoundPrefix(rulesPath)
	m.SetNotFoundPrefix(ruleMetricsPath)
	if got := collectRules(t, m); len(got) != 0 {
		t.Errorf("samples = %v, want none on PPDM < 20.3", got)
	}
}

func TestRulesOneEndpointNotFound(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	m.SetNotFoundPrefix(rulesPath)
	b, _ := os.ReadFile("testdata/protection-policy-rule-metrics.json")
	m.SetJSONPrefix(ruleMetricsPath, string(b))
	got := collectRules(t, m)
	if len(got) != 2 {
		t.Fatalf("samples = %d, want the 2 assigned-assets samples", len(got))
	}
	for _, s := range got {
		if s.Name != "ppdm_rule_assigned_assets" {
			t.Errorf("unexpected sample %s", s.Name)
		}
	}
}

func TestRulesErrorPropagates(t *testing.T) {
	_, err := Rules{}.Collect(context.Background(), ppdmclient.NewMock("ppdm01")) // nothing registered: plain error
	if err == nil || errors.Is(err, ppdmclient.ErrNotFound) {
		t.Errorf("err = %v, want a real (non-404) error", err)
	}
}

func TestRulesEmptyEmitsNoCount(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix(rulesPath, `{"page":{"number":0,"totalPages":1},"content":[]}`)
	m.SetJSONPrefix(ruleMetricsPath, `{"page":{"number":0,"totalPages":1},"content":[]}`)
	if got := collectRules(t, m); len(got) != 0 {
		t.Errorf("samples = %v, want none", got)
	}
}

func TestRulesViaPrometheusAndOTLP(t *testing.T) {
	var samples []Sample
	for _, s := range collectRules(t, ruleMock(t)) {
		samples = append(samples, s.WithServer("ppdm01"))
	}
	store := NewSnapshotStore()
	store.Store(&Snapshot{BuiltAt: time.Now(), Servers: []*ServerSnapshot{{Server: "ppdm01", OK: true, Samples: samples}}})

	want := `
# HELP ppdm_rule_assigned_assets PPDM metric ppdm_rule_assigned_assets
# TYPE ppdm_rule_assigned_assets gauge
ppdm_rule_assigned_assets{policy="Gold",rule="vm-gold",server="ppdm01"} 12
ppdm_rule_assigned_assets{policy="Silver",rule="fs-never-run",server="ppdm01"} 0
`
	if err := testutil.CollectAndCompare(NewPromCollector(store), strings.NewReader(want), "ppdm_rule_assigned_assets"); err != nil {
		t.Fatal(err)
	}

	reader := metric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	if err := exp.EnsureInstruments(); err != nil {
		t.Fatalf("EnsureInstruments: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			found[m.Name] = true
		}
	}
	for _, n := range []string{"ppdm_rule_count", "ppdm_rule_last_run_age_seconds", "ppdm_rule_assigned_assets"} {
		if !found[n] {
			t.Errorf("%s not observed via OTLP ManualReader", n)
		}
	}
}
