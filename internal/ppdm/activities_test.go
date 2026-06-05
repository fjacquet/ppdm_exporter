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
}
