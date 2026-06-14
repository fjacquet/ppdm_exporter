package ppdm

import (
	"context"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// asset is one /api/v2/assets content item, validated against the 20.1.0 Asset schema (ADR-0010).
type asset struct {
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ProtectionStatus      string `json:"protectionStatus"`
	LastAvailableCopyTime string `json:"lastAvailableCopyTime"` // RFC3339 or "" (null)
}

// Assets aggregates protection rollups plus a bounded per-asset SLA-age series.
// AgeThreshold gates the per-asset emission; now is injectable for tests (defaults to time.Now).
type Assets struct {
	AgeThreshold time.Duration
	now          func() time.Time
}

func (Assets) Name() string { return "assets" }

func (a Assets) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a Assets) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	assets, err := ppdmclient.GetAll[asset](ctx, c, "/api/v2/assets", 500)
	if err != nil {
		return nil, err
	}
	now := a.clock()
	type k struct{ typ, status string }
	counts := map[k]float64{}
	var unprotected float64
	var ageSamples []Sample
	for _, as := range assets {
		counts[k{as.Type, as.ProtectionStatus}]++
		if as.ProtectionStatus != "PROTECTED" {
			unprotected++
		}
		if as.LastAvailableCopyTime == "" {
			continue // never-copied: surfaced by the rollups, no age to report
		}
		t, perr := time.Parse(time.RFC3339, as.LastAvailableCopyTime)
		if perr != nil {
			continue
		}
		age := now.Sub(t).Seconds()
		if as.ProtectionStatus != "PROTECTED" || age > a.AgeThreshold.Seconds() {
			ageSamples = append(ageSamples, Sample{Name: "ppdm_asset_last_copy_age_seconds", Value: age,
				Labels: []Label{
					{Key: "asset", Value: as.Name},
					{Key: "type", Value: as.Type},
					{Key: "protection_status", Value: as.ProtectionStatus},
				}})
		}
	}
	out := []Sample{{Name: "ppdm_asset_unprotected", Value: unprotected,
		Labels: []Label{{Key: "type", Value: ""}, {Key: "protection_status", Value: ""}}}}
	for key, v := range counts {
		out = append(out, Sample{Name: "ppdm_asset_count", Value: v, Labels: []Label{
			{Key: "type", Value: key.typ}, {Key: "protection_status", Value: key.status},
		}})
	}
	return append(out, ageSamples...), nil
}
