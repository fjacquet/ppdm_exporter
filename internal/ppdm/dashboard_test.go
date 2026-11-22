package ppdm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Unavailable rule data (PPDM < 20.3, never-run rules) must render as N/A, never 0 (ADR-0017).
func TestRulePanelsShowNAWhenNoData(t *testing.T) {
	b, err := os.ReadFile("../../grafana/dashboards/ppdm-overview.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Panels []struct {
			Title       string `json:"title"`
			FieldConfig struct {
				Defaults struct {
					NoValue string `json:"noValue"`
				} `json:"defaults"`
			} `json:"fieldConfig"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range d.Panels {
		for _, tg := range p.Targets {
			if strings.Contains(tg.Expr, "ppdm_rule_") {
				n++
				if p.FieldConfig.Defaults.NoValue != "N/A" {
					t.Errorf("panel %q: noValue = %q, want N/A", p.Title, p.FieldConfig.Defaults.NoValue)
				}
			}
		}
	}
	if n < 3 {
		t.Errorf("rule panels = %d, want at least 3", n)
	}
}
