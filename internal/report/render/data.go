// Package render builds and renders per-tenant backup-assurance reports (HTML + PDF) over the
// report store's compliance and rule_321110 views.
package render

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
)

// ErrNoData is returned by Build when a tenant has no captured assets (→ HTTP 404, distinct from
// a database failure → 500). errUnsupportedFormat is shared by the CLI and HTTP format checks.
var (
	ErrNoData            = errors.New("no captured assets for tenant")
	errUnsupportedFormat = errors.New("unsupported format (want html or pdf)")
)

// ReportData is everything a renderer needs for one tenant's current-snapshot report.
type ReportData struct {
	Tenant      string
	BrandName   string
	GeneratedAt time.Time
	Summary     report.Summary
	Compliance  []report.ComplianceRow
	Rule321     []report.Rule321Row
}

// CompliantPercent is the share of assets meeting every SLA rule, 0..100 (0 when no assets).
func (d ReportData) CompliantPercent() int {
	if d.Summary.TotalAssets == 0 {
		return 0
	}
	return d.Summary.CompliantAssets * 100 / d.Summary.TotalAssets
}

// BadgeText renders the 3-2-1-1-0 verdict as a word.
func (d ReportData) BadgeText() string {
	if d.Summary.BadgePass {
		return "PASS"
	}
	return "FAIL"
}

// Build assembles a tenant's report from the store. now is injected (testable). It errors when
// the tenant has no assets — there is nothing to assure.
func Build(ctx context.Context, st *report.Store, tenant, brand string, now time.Time) (ReportData, error) {
	sum, err := st.ReportSummary(ctx, tenant)
	if err != nil {
		return ReportData{}, fmt.Errorf("summary: %w", err)
	}
	if sum.TotalAssets == 0 {
		return ReportData{}, fmt.Errorf("%w: %q", ErrNoData, tenant)
	}
	comp, err := st.ComplianceRows(ctx, tenant)
	if err != nil {
		return ReportData{}, fmt.Errorf("compliance rows: %w", err)
	}
	rule, err := st.Rule321Rows(ctx, tenant)
	if err != nil {
		return ReportData{}, fmt.Errorf("rule rows: %w", err)
	}
	return ReportData{
		Tenant: tenant, BrandName: brand, GeneratedAt: now,
		Summary: sum, Compliance: comp, Rule321: rule,
	}, nil
}
