package ppdm

import (
	"context"
	"net/url"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// activity is the shape of one /api/v2/activities content item, validated against the
// 20.1.0 Activity schema (docs/swagger/9765, ADR-0010). result.bytesTransferred is
// cumulative + summable (PDF-confirmed on PROTECT activities).
type activity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	State       string `json:"state"`
	CreateTime  string `json:"createTime"` // RFC3339
	EndTime     string `json:"endTime"`    // RFC3339, or "" / null when still running
	Result      struct {
		Status           string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
	ProtectionPolicy struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"`
	Asset struct {
		Name string `json:"name"`
	} `json:"asset"`
}

// status returns the terminal result status, falling back to the lifecycle state for
// activities that have not finished (running/queued have no result.status yet).
func (a activity) status() string {
	if a.Result.Status != "" {
		return a.Result.Status
	}
	return a.State
}

// Activities aggregates PPDM job outcome counts and total bytes within a lookback window.
// When PerJob is set it also emits per-job detail metrics (opt-in; higher cardinality).
type Activities struct {
	Lookback time.Duration
	PerJob   bool
}

func (Activities) Name() string { return "activities" }

func (a Activities) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	since := time.Now().Add(-a.Lookback).UTC().Format(time.RFC3339)
	// The filter value carries spaces and quotes, so it must be URL-encoded; GetAll
	// then appends &page=/&pageSize= with the correct separator.
	path := "/api/v2/activities?filter=" + url.QueryEscape(`createTime ge "`+since+`"`)
	acts, err := ppdmclient.GetAll[activity](ctx, c, path, 500)
	if err != nil {
		return nil, err
	}

	type catKey struct{ category, status string }
	counts := map[catKey]float64{}
	bytesTotal := map[string]float64{} // cumulative result.bytesTransferred summed per category
	for _, act := range acts {
		counts[catKey{act.Category, act.status()}]++
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
	if a.PerJob {
		out = append(out, perJobSamples(acts)...)
	}
	return out, nil
}

// perJobSamples emits, for each activity, an info series (value 1, descriptive labels)
// plus value series for bytes and duration keyed by activity_id. A Grafana table joins
// them by activity_id into a per-job backup report.
func perJobSamples(acts []activity) []Sample {
	var out []Sample
	for _, act := range acts {
		out = append(out, Sample{Name: "ppdm_activity_info", Value: 1, Labels: []Label{
			{Key: "activity_id", Value: act.ID},
			{Key: "name", Value: act.Name},
			{Key: "category", Value: act.Category},
			{Key: "subcategory", Value: act.Subcategory},
			{Key: "result_status", Value: act.status()},
			{Key: "asset", Value: act.Asset.Name},
			{Key: "policy", Value: act.ProtectionPolicy.Name},
		}})
		out = append(out, Sample{Name: "ppdm_activity_job_bytes", Value: act.Result.BytesTransferred,
			Labels: []Label{{Key: "activity_id", Value: act.ID}}})
		if d, ok := jobDuration(act); ok {
			out = append(out, Sample{Name: "ppdm_activity_job_duration_seconds", Value: d,
				Labels: []Label{{Key: "activity_id", Value: act.ID}}})
		}
	}
	return out
}

// jobDuration returns endTime-createTime in seconds when both timestamps parse.
func jobDuration(act activity) (float64, bool) {
	if act.CreateTime == "" || act.EndTime == "" {
		return 0, false
	}
	start, err1 := time.Parse(time.RFC3339, act.CreateTime)
	end, err2 := time.Parse(time.RFC3339, act.EndTime)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return end.Sub(start).Seconds(), true
}
