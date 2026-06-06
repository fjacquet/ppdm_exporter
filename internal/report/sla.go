package report

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	log "github.com/sirupsen/logrus"
)

// objective is one stage of a PPDM protection policy. schedule.interval is the backup cadence
// (RPO) and retention.interval the retention window, both ISO-8601 durations. Provisional shape.
type objective struct {
	Type     string `json:"type"`
	Schedule struct {
		Interval string `json:"interval"`
	} `json:"schedule"`
	Retention struct {
		Interval string `json:"interval"`
	} `json:"retention"`
}

// deriveTarget pulls RPO (seconds) and retention (days) from a policy's objectives JSON,
// preferring the BACKUP stage. A field that is absent or unparseable comes back as 0, signalling
// the caller to keep the configured default for it — derivation is best-effort, never an error.
func deriveTarget(raw []byte) (rpoSeconds int64, retentionDays int) {
	var objs []objective
	if err := json.Unmarshal(raw, &objs); err != nil || len(objs) == 0 {
		return 0, 0
	}
	o := objs[0]
	for _, x := range objs {
		if x.Type == "BACKUP" {
			o = x
			break
		}
	}
	if d, err := parseISODuration(o.Schedule.Interval); err == nil {
		rpoSeconds = int64(d / time.Second)
	}
	if d, err := parseISODuration(o.Retention.Interval); err == nil {
		retentionDays = int(d / (24 * time.Hour))
	}
	return rpoSeconds, retentionDays
}

// ResolveTargets computes the effective SLA targets for a tenant and upserts them into
// sla_targets. It writes three layers of rows that the compliance view picks the most specific
// of: the per-tenant default, one per captured protection policy (derived from objectives), and
// one per matching config override. It never blocks capture — a missing objective just falls back
// to the configured default.
func (c *Capturer) ResolveTargets(ctx context.Context, tenant string, cfg config.Compliance) error {
	grace := int64(cfg.Grace / time.Second)
	def := SLATarget{
		Tenant:        tenant,
		RPOSeconds:    int64(cfg.Defaults.RPOHours) * 3600,
		RetentionDays: cfg.Defaults.RetentionDays,
		MinCopies:     cfg.Defaults.MinCopies,
		GraceSeconds:  grace,
		Source:        "default",
	}
	targets := []SLATarget{def}

	policies, err := c.store.CapturedPolicies(ctx, tenant)
	if err != nil {
		return err
	}
	// Per-policy rows, keyed by policy name; remembered so an override naming a policy inherits it.
	byPolicy := map[string]SLATarget{}
	for _, p := range policies {
		t := def
		t.PolicyName = p.Name
		t.Source = "policy"
		rpo, ret := deriveTarget(p.Objectives)
		switch {
		case rpo == 0 && ret == 0:
			log.WithFields(log.Fields{"tenant": tenant, "policy": p.Name}).
				Debug("no parseable SLA objective; using defaults")
		default:
			if rpo > 0 {
				t.RPOSeconds = rpo
			}
			if ret > 0 {
				t.RetentionDays = ret
			}
		}
		byPolicy[p.Name] = t
		targets = append(targets, t)
	}

	// Per-override rows: each non-zero override field overlays the policy-derived row (when the
	// override names a policy) else the default. An asset-type-only override's unset fields fall
	// back to tenant defaults, not per-policy derivation.
	for _, o := range cfg.Overrides {
		if o.Tenant != "" && o.Tenant != tenant {
			continue
		}
		base := def
		if o.PolicyName != "" {
			if pt, ok := byPolicy[o.PolicyName]; ok {
				base = pt
			}
		}
		t := base
		t.Tenant = tenant
		t.AssetType = o.AssetType
		t.PolicyName = o.PolicyName
		t.GraceSeconds = grace
		t.Source = "override"
		if o.RPOHours > 0 {
			t.RPOSeconds = int64(o.RPOHours) * 3600
		}
		if o.RetentionDays > 0 {
			t.RetentionDays = o.RetentionDays
		}
		if o.MinCopies > 0 {
			t.MinCopies = o.MinCopies
		}
		targets = append(targets, t)
	}

	return c.store.UpsertSLATargets(ctx, targets)
}
