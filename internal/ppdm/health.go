package ppdm

import (
	"context"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

type healthEntity struct {
	Name      string `json:"name"`      // provisional
	Component string `json:"component"` // provisional
	Status    string `json:"status"`    // provisional (enum unconfirmed)
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
	entities, err := ppdmclient.GetAll[healthEntity](ctx, c, "/api/v3/health-entities", 500)
	if err != nil {
		return nil, err
	}
	alerts, err := ppdmclient.GetAll[alert](ctx, c, "/api/v2/alerts", 500)
	if err != nil {
		return nil, err
	}
	var out []Sample
	for _, e := range entities {
		v := 0.0
		if e.Status == "OK" {
			v = 1
		}
		out = append(out, Sample{Name: "ppdm_health_entity_status", Value: v, Labels: []Label{
			{Key: "entity", Value: e.Name}, {Key: "component", Value: e.Component},
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
