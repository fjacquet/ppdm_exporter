package ppdm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestActivitiesCollect(t *testing.T) {
	body, err := os.ReadFile("testdata/activities.json")
	if err != nil {
		t.Fatal(err)
	}
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", string(body))

	got, err := Activities{Lookback: 24 * time.Hour}.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	seen := map[string]float64{}
	for _, s := range got {
		key := s.Name + "|" + s.LabelValue("category") + "|" + s.LabelValue("result_status")
		seen[key] += s.Value
	}
	if seen["ppdm_activity_count|PROTECT|SUCCESS"] != 1 {
		t.Errorf("PROTECT/SUCCESS count = %v, want 1", seen["ppdm_activity_count|PROTECT|SUCCESS"])
	}
	if seen["ppdm_activity_count|PROTECT|FAILED"] != 1 {
		t.Errorf("PROTECT/FAILED count = %v, want 1", seen["ppdm_activity_count|PROTECT|FAILED"])
	}
	// Summed result.bytesTransferred for PROTECT = 1048576 + 0.
	if seen["ppdm_activity_bytes_total|PROTECT|"] != 1048576 {
		t.Errorf("PROTECT bytes total = %v, want 1048576", seen["ppdm_activity_bytes_total|PROTECT|"])
	}
	// PerJob off by default: no per-job series.
	for _, s := range got {
		if s.Name == "ppdm_activity_info" {
			t.Fatalf("ppdm_activity_info emitted with PerJob disabled")
		}
	}
}

func TestActivitiesPerJob(t *testing.T) {
	body, err := os.ReadFile("testdata/activities.json")
	if err != nil {
		t.Fatal(err)
	}
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/activities", string(body))

	got, err := Activities{Lookback: 24 * time.Hour, PerJob: true}.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	info := map[string]activitySample{}
	bytesByID := map[string]float64{}
	durByID := map[string]float64{}
	for _, s := range got {
		switch s.Name {
		case "ppdm_activity_info":
			info[s.LabelValue("activity_id")] = activitySample{
				asset: s.LabelValue("asset"), policy: s.LabelValue("policy"), status: s.LabelValue("result_status"),
			}
		case "ppdm_activity_job_bytes":
			bytesByID[s.LabelValue("activity_id")] = s.Value
		case "ppdm_activity_job_duration_seconds":
			durByID[s.LabelValue("activity_id")] = s.Value
		}
	}
	if len(info) != 3 {
		t.Fatalf("ppdm_activity_info series = %d, want 3", len(info))
	}
	if info["act-1"].asset != "vm-app01" || info["act-1"].policy != "Gold-VM" || info["act-1"].status != "SUCCESS" {
		t.Errorf("act-1 info = %+v", info["act-1"])
	}
	if bytesByID["act-1"] != 1048576 {
		t.Errorf("act-1 bytes = %v, want 1048576", bytesByID["act-1"])
	}
	if durByID["act-1"] != 252 { // 01:04:12 - 01:00:00
		t.Errorf("act-1 duration = %v, want 252", durByID["act-1"])
	}
	// act-3 is still running (completedAt null) -> no duration series.
	if _, ok := durByID["act-3"]; ok {
		t.Errorf("act-3 (running) should have no duration series")
	}
}

type activitySample struct{ asset, policy, status string }
