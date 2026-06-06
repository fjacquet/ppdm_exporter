package delivery

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildMessage(t *testing.T) {
	m, err := buildMessage("assurance@example.com", "Report — acme [FAIL]",
		[]string{"ops@acme.com"}, []byte("<h1>Report</h1>"), "acme-report.pdf", []byte("%PDF-1.4 test"))
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	out := buf.String()
	// The subject containing a non-ASCII em-dash (—) is RFC2047-encoded by go-mail;
	// check for the encoded form as well as the ASCII parts that appear unencoded.
	for _, want := range []string{"=E2=80=94", "acme", "ops@acme.com", "text/plain", "text/html", "application/pdf", "acme-report.pdf", "Report", "[FAIL]"} {
		if !strings.Contains(out, want) {
			t.Errorf("message missing %q", want)
		}
	}
}
