package ppdm

import "testing"

func TestSampleLabelValueLookup(t *testing.T) {
	s := Sample{Name: "ppdm_asset_count", Value: 42,
		Labels: []Label{{Key: "server", Value: "ppdm01"}}}
	if got := s.LabelValue("server"); got != "ppdm01" {
		t.Fatalf("LabelValue(server) = %q, want ppdm01", got)
	}
	if got := s.LabelValue("missing"); got != "" {
		t.Fatalf("LabelValue(missing) = %q, want empty", got)
	}
}

func TestWithServerPrependsLabel(t *testing.T) {
	s := Sample{Name: "x", Labels: []Label{{Key: "category", Value: "PROTECT"}}}
	out := s.WithServer("ppdm01")
	if out.Labels[0].Key != "server" || out.Labels[0].Value != "ppdm01" {
		t.Fatalf("WithServer did not prepend server label: %+v", out.Labels)
	}
}
