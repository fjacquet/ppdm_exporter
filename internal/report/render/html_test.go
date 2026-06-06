package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
)

func sampleData() ReportData {
	return ReportData{
		Tenant: "acme", BrandName: "Acme Co",
		GeneratedAt: time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
		Summary:     report.Summary{TotalAssets: 2, CompliantAssets: 1, CopiesFailures: 1, BadgePass: false},
		Compliance: []report.ComplianceRow{
			{AssetID: "x", AssetName: "<script>alert(1)</script>", AssetType: "VMWARE_VIRTUAL_MACHINE",
				Compliant: false, Reasons: "copies"},
		},
		Rule321: []report.Rule321Row{
			{AssetID: "x", AssetName: "vm", ImmutableOk: true, RulePass: false, CopiesCount: 1},
		},
	}
}

func TestRenderHTMLContentAndEscaping(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderHTML(&buf, sampleData()); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Acme Co", "acme", "FAIL", "50%"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// html/template must escape the hostile asset name — no raw <script> in the output.
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("hostile asset name was not escaped (XSS)")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("expected escaped asset name in output")
	}
}
