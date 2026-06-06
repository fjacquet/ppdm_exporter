package main

import "testing"

// formatExt maps a --format flag to a file extension; "" defaults to html.
func TestFormatExt(t *testing.T) {
	cases := map[string]string{"": "html", "html": "html", "pdf": "pdf"}
	for in, want := range cases {
		if got, err := formatExt(in); err != nil || got != want {
			t.Errorf("formatExt(%q) = %q,%v want %q", in, got, err, want)
		}
	}
	if _, err := formatExt("docx"); err == nil {
		t.Error("expected error for unknown format")
	}
}
