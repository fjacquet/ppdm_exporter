package render

import "testing"

// FormatExt maps a --format/?format value to a file extension; "" defaults to html.
func TestFormatExt(t *testing.T) {
	cases := map[string]string{"": "html", "html": "html", "pdf": "pdf"}
	for in, want := range cases {
		if got, err := FormatExt(in); err != nil || got != want {
			t.Errorf("FormatExt(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := FormatExt("docx"); err == nil {
		t.Error("expected error for unknown format")
	}
}
