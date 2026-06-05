package ppdm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestAssetsCollect(t *testing.T) {
	body, _ := os.ReadFile("testdata/assets.json")
	m := ppdmclient.NewMock("ppdm01")
	m.SetJSONPrefix("/api/v2/assets", string(body))
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	a := Assets{AgeThreshold: 24 * time.Hour, now: func() time.Time { return now }}
	got, err := a.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	seen := map[string]float64{}
	for _, s := range got {
		seen[s.Name+"|"+s.LabelValue("type")+"|"+s.LabelValue("protection_status")] += s.Value
	}
	if seen["ppdm_asset_count|VMWARE_VIRTUAL_MACHINE|PROTECTED"] != 1 {
		t.Errorf("VM/PROTECTED = %v, want 1", seen["ppdm_asset_count|VMWARE_VIRTUAL_MACHINE|PROTECTED"])
	}
	if seen["ppdm_asset_unprotected||"] != 1 {
		t.Errorf("unprotected rollup = %v, want 1", seen["ppdm_asset_unprotected||"])
	}
	// Bounded SLA series: only nas01 (stale PROTECTED) appears; vm-app01 (healthy) is suppressed.
	var ageAssets []string
	for _, s := range got {
		if s.Name == "ppdm_asset_last_copy_age_seconds" {
			ageAssets = append(ageAssets, s.LabelValue("asset"))
			if s.LabelValue("asset") == "nas01" && s.Value != 4*86400 {
				t.Errorf("nas01 age = %v, want %v", s.Value, 4*86400)
			}
		}
	}
	if len(ageAssets) != 1 || ageAssets[0] != "nas01" {
		t.Errorf("age series assets = %v, want [nas01] only", ageAssets)
	}
}
