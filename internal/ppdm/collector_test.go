package ppdm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestCollectOncePopulatesSnapshot(t *testing.T) {
	body, _ := os.ReadFile("testdata/activities.json")
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", string(body))

	store := NewSnapshotStore()
	col := NewCollector([]ppdmclient.Client{m}, []ResourceCollector{Activities{Lookback: time.Hour}}, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	if len(snap.Servers) != 1 {
		t.Fatalf("servers = %d, want 1", len(snap.Servers))
	}
	sv := snap.Servers[0]
	if !sv.OK {
		t.Fatalf("server not OK: %s", sv.Err)
	}
	var up float64
	for _, s := range sv.Samples {
		if s.Name == "ppdm_up" {
			up = s.Value
		}
		if s.LabelValue("server") != "ppdm01" {
			t.Errorf("sample %s missing server label", s.Name)
		}
	}
	if up != 1 {
		t.Fatalf("ppdm_up = %v, want 1", up)
	}
}

func TestCollectServerDegradesOnError(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01") // no paths -> activities collector errors
	store := NewSnapshotStore()
	col := NewCollector([]ppdmclient.Client{m}, []ResourceCollector{Activities{Lookback: time.Hour}}, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	sv := snap.Servers[0]
	if sv.OK {
		t.Fatal("server should be degraded when a collector fails")
	}
	var collUp, up float64 = -1, -1
	for _, s := range sv.Samples {
		if s.Name == "ppdm_collector_up" && s.LabelValue("collector") == "activities" {
			collUp = s.Value
		}
		if s.Name == "ppdm_up" {
			up = s.Value
		}
	}
	if collUp != 0 {
		t.Fatalf("ppdm_collector_up{activities} = %v, want 0", collUp)
	}
	if up != 0 {
		t.Fatalf("ppdm_up = %v, want 0", up)
	}
}
