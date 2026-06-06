package render

import (
	"fmt"
	"io"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

// yn renders a boolean as a compact PDF cell value.
func yn(b bool) string {
	if b {
		return "OK"
	}
	return "X"
}

// RenderPDF writes a structured, tabular PDF of the same ReportData (independent layout from the
// HTML — the cost of a browser-free, pure-Go renderer).
func RenderPDF(w io.Writer, d ReportData) error {
	m := maroto.New()

	m.AddRow(10, text.NewCol(12, d.BrandName, props.Text{Style: fontstyle.Bold, Size: 16}))
	m.AddRow(6, text.NewCol(12, fmt.Sprintf("Tenant: %s · Generated %s",
		d.Tenant, d.GeneratedAt.Format("2006-01-02 15:04 MST")), props.Text{Size: 9}))
	m.AddRow(8, text.NewCol(12, fmt.Sprintf("Compliant %d/%d (%d%%) · 3-2-1-1-0 badge: %s",
		d.Summary.CompliantAssets, d.Summary.TotalAssets, d.CompliantPercent(), d.BadgeText()),
		props.Text{Style: fontstyle.Bold, Size: 11}))

	m.AddRow(7, text.NewCol(12, "SLA compliance", props.Text{Style: fontstyle.Bold, Size: 12}))
	m.AddRow(6,
		text.NewCol(5, "Asset", props.Text{Style: fontstyle.Bold}),
		text.NewCol(3, "Policy", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Compliant", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Reasons", props.Text{Style: fontstyle.Bold}),
	)
	for _, r := range d.Compliance {
		m.AddRow(5,
			text.NewCol(5, r.AssetName),
			text.NewCol(3, r.PolicyName),
			text.NewCol(2, yn(r.Compliant)),
			text.NewCol(2, r.Reasons),
		)
	}

	m.AddRow(7, text.NewCol(12, "3-2-1-1-0 backup rule (2-media/1-offsite are provisional heuristics)",
		props.Text{Style: fontstyle.Bold, Size: 12}))
	m.AddRow(6,
		text.NewCol(4, "Asset", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Copies", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Media", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Immutable", props.Text{Style: fontstyle.Bold}),
		text.NewCol(2, "Rule", props.Text{Style: fontstyle.Bold}),
	)
	for _, r := range d.Rule321 {
		m.AddRow(5,
			text.NewCol(4, r.AssetName),
			text.NewCol(2, fmt.Sprintf("%d", r.CopiesCount)),
			text.NewCol(2, fmt.Sprintf("%d", r.DistinctMedia)),
			text.NewCol(2, yn(r.ImmutableOk)),
			text.NewCol(2, yn(r.RulePass)),
		)
	}

	doc, err := m.Generate()
	if err != nil {
		return fmt.Errorf("generate pdf: %w", err)
	}
	_, err = w.Write(doc.GetBytes())
	return err
}
