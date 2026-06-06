package render

import (
	"bytes"
	"testing"
)

func TestRenderPDFProducesPDFBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderPDF(&buf, sampleData()); err != nil {
		t.Fatalf("RenderPDF: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty PDF")
	}
	if got := buf.Bytes()[:5]; string(got) != "%PDF-" {
		t.Fatalf("not a PDF, header = %q", got)
	}
}
