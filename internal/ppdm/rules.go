package ppdm

import (
	"context"
	"errors"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// rule is one /api/v3/rules content item (20.3.0 Rule). lastRunAt is RFC3339 or empty/null.
type rule struct {
	Name      string `json:"name"`
	Status    string `json:"status"` // ACTIVE | CREATING | DELETING
	LastRunAt string `json:"lastRunAt"`
}

// ruleMetric is one /api/v3/protection-policy-rule-metrics content item (20.3.0).
type ruleMetric struct {
	ProtectionPolicyName string  `json:"protectionPolicyName"`
	AssignedAssetCount   float64 `json:"assignedAssetCount"`
	Rule                 struct {
		Name string `json:"name"`
	} `json:"rule"`
}

// Rules collects protection-rule metrics (PPDM 20.3.0+). On older servers the endpoints
// answer 404: that endpoint then yields no samples and no error, so the dashboard shows
// N/A instead of a misleading zero (ADR-0017).
type Rules struct {
	now func() time.Time
}

func (Rules) Name() string { return "rules" }

func (r Rules) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r Rules) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	var out []Sample

	rules, err := ppdmclient.GetAll[rule](ctx, c, "/api/v3/rules", 500)
	if err != nil && !errors.Is(err, ppdmclient.ErrNotFound) {
		return nil, err
	}
	now := r.clock()
	counts := map[string]float64{}
	var ages []Sample
	for _, ru := range rules {
		counts[ru.Status]++
		if ru.LastRunAt == "" {
			continue // never run: no age to report
		}
		t, perr := time.Parse(time.RFC3339, ru.LastRunAt)
		if perr != nil {
			continue
		}
		ages = append(ages, Sample{Name: "ppdm_rule_last_run_age_seconds", Value: now.Sub(t).Seconds(),
			Labels: []Label{{Key: "rule", Value: ru.Name}}})
	}
	for status, v := range counts {
		out = append(out, Sample{Name: "ppdm_rule_count", Value: v, Labels: []Label{{Key: "status", Value: status}}})
	}
	out = append(out, ages...)

	metrics, err := ppdmclient.GetAll[ruleMetric](ctx, c, "/api/v3/protection-policy-rule-metrics", 500)
	if err != nil && !errors.Is(err, ppdmclient.ErrNotFound) {
		return nil, err
	}
	for _, m := range metrics {
		out = append(out, Sample{Name: "ppdm_rule_assigned_assets", Value: m.AssignedAssetCount, Labels: []Label{
			{Key: "policy", Value: m.ProtectionPolicyName}, {Key: "rule", Value: m.Rule.Name},
		}})
	}
	return out, nil
}
