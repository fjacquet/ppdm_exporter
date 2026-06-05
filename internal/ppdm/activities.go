package ppdm

import (
	"context"
	"net/url"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// activity is the shape of one /api/v2/activities content item.
// state/result.status confirmed via the Apache-2.0 dell.ppdm.psm1; result.bytesTransferred
// is cumulative + summable (PDF-confirmed on PROTECT activities).
type activity struct {
	Category string `json:"category"`
	State    string `json:"state"`
	Result   struct {
		Status           string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
}

// Activities aggregates PPDM job outcome counts and total bytes within a lookback window.
type Activities struct{ Lookback time.Duration }

func (Activities) Name() string { return "activities" }

func (a Activities) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	since := time.Now().Add(-a.Lookback).UTC().Format(time.RFC3339)
	// The filter value carries spaces and quotes, so it must be URL-encoded; GetAll
	// then appends &page=/&pageSize= with the correct separator.
	path := "/api/v2/activities?filter=" + url.QueryEscape(`createdAt ge "`+since+`"`)
	acts, err := ppdmclient.GetAll[activity](ctx, c, path, 500)
	if err != nil {
		return nil, err
	}

	type catKey struct{ category, status string }
	counts := map[catKey]float64{}
	bytesTotal := map[string]float64{} // cumulative result.bytesTransferred summed per category
	for _, act := range acts {
		status := act.Result.Status
		if status == "" {
			status = act.State // running/queued activities have no terminal status yet
		}
		counts[catKey{act.Category, status}]++
		bytesTotal[act.Category] += act.Result.BytesTransferred
	}

	var out []Sample
	for k, v := range counts {
		out = append(out, Sample{Name: "ppdm_activity_count", Value: v, Labels: []Label{
			{Key: "category", Value: k.category}, {Key: "result_status", Value: k.status},
		}})
	}
	for cat, v := range bytesTotal {
		out = append(out, Sample{Name: "ppdm_activity_bytes_total", Value: v,
			Labels: []Label{{Key: "category", Value: cat}, {Key: "result_status", Value: ""}}})
	}
	return out, nil
}
