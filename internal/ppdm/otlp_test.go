package ppdm

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestOTLPObservesSnapshot(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{BuiltAt: time.Now(), Servers: []*ServerSnapshot{{
		Server: "ppdm01", OK: true,
		Samples: []Sample{{Name: "ppdm_alert_count",
			Labels: []Label{{"server", "ppdm01"}, {"severity", "CRITICAL"}, {"ack_state", "NOT_ACKNOWLEDGED"}}, Value: 2}},
	}}})
	reader := metric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	if err := exp.EnsureInstruments(); err != nil {
		t.Fatalf("EnsureInstruments: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "ppdm_alert_count" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("ppdm_alert_count not observed via OTLP ManualReader")
	}
}
