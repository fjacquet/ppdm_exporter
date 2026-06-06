package render

import (
	_ "embed"
	"html/template"
	"io"
)

//go:embed report.html.tmpl
var htmlTmplSrc string

var htmlTmpl = template.Must(template.New("report").Parse(htmlTmplSrc))

// RenderHTML writes the branded HTML report. html/template auto-escapes all data, so a hostile
// asset name cannot inject markup.
func RenderHTML(w io.Writer, d ReportData) error {
	return htmlTmpl.Execute(w, d)
}
