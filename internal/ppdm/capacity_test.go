package ppdm

import (
	"context"
	"os"
	"testing"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestCapacityCollect(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	for _, f := range []struct{ prefix, file string }{
		{"/api/v2/datadomain-mtrees", "testdata/datadomain-mtrees.json"},
		{"/api/v2/storage-systems", "testdata/storage-systems.json"},
	} {
		body, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		m.SetJSONPrefix(f.prefix, string(body))
	}

	got, err := Capacity{}.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	seen := map[string]float64{}
	for _, s := range got {
		seen[s.Name+"|"+s.LabelValue("storage_unit")+"|"+s.LabelValue("storage_system")] = s.Value
	}
	want := map[string]float64{
		"ppdm_storage_unit_physical_capacity_bytes|/data/col1/su-policy-a|": 3220957036544,
		"ppdm_storage_unit_physical_used_bytes|/data/col1/su-policy-a|":     342523641856,
		"ppdm_storage_system_total_bytes||ddve-01":                          3220957036544,
		"ppdm_storage_system_used_bytes||ddve-01":                           342523641856,
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s = %v, want %v", k, seen[k], v)
		}
	}
	if _, ok := seen["ppdm_storage_unit_logical_used_bytes|/data/col1/su-policy-a|"]; ok {
		t.Error("ppdm_storage_unit_logical_used_bytes should no longer be emitted")
	}
}
