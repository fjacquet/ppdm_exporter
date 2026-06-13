package ppdm

import (
	"context"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// healthResult is one /api/v3/health-results content item (20.1.0 HealthResult, ADR-0010).
// status is the live grade; the entity name + categories come from healthEntityRef.
type healthResult struct {
	Status          string `json:"status"` // OK | WARNING | CRITICAL
	HealthEntityRef struct {
		Name       string   `json:"name"`
		Categories []string `json:"categories"`
	} `json:"healthEntityRef"`
}

type alert struct {
	Severity        string `json:"severity"` // PDF-confirmed: CRITICAL/WARNING/INFORMATIONAL
	Acknowledgement struct {
		// acknowledgeState confirmed by the Apache-2.0 dell.ppdm.psm1; enum values provisional.
		AcknowledgeState string `json:"acknowledgeState"`
	} `json:"acknowledgement"`
}

// Health collects PPDM health-entity status and alert counts.
type Health struct{}

func (Health) Name() string { return "health" }

func (Health) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	results, err := ppdmclient.GetAll[healthResult](ctx, c, "/api/v3/health-results", 500)
	if err != nil {
		return nil, err
	}
	alerts, err := ppdmclient.GetAll[alert](ctx, c, "/api/v2/alerts", 500)
	if err != nil {
		return nil, err
	}
	var out []Sample
	for _, r := range results {
		v := 0.0
		if r.Status == "OK" {
			v = 1
		}
		// component label = the primary (first) category; extra categories are intentionally ignored (ADR-0010).
		comp := ""
		if len(r.HealthEntityRef.Categories) > 0 {
			comp = r.HealthEntityRef.Categories[0]
		}
		out = append(out, Sample{Name: "ppdm_health_entity_status", Value: v, Labels: []Label{
			{Key: "entity", Value: r.HealthEntityRef.Name}, {Key: "component", Value: comp},
		}})
	}
	type ak struct{ severity, ackState string }
	counts := map[ak]float64{}
	for _, a := range alerts {
		counts[ak{a.Severity, a.Acknowledgement.AcknowledgeState}]++
	}
	for k, v := range counts {
		out = append(out, Sample{Name: "ppdm_alert_count", Value: v, Labels: []Label{
			{Key: "severity", Value: k.severity}, {Key: "ack_state", Value: k.ackState},
		}})
	}
	return out, nil
}
