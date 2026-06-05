package report

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	got, ok := parseTime("2026-06-05T01:04:12Z")
	if !ok || got.UTC() != time.Date(2026, 6, 5, 1, 4, 12, 0, time.UTC) {
		t.Fatalf("parseTime = %v ok=%v", got, ok)
	}
	if _, ok := parseTime(""); ok {
		t.Fatal("empty string should parse as not-ok")
	}
}

func TestJobAssetAndStatus(t *testing.T) {
	j := Job{State: "RUNNING"}
	if j.status() != "RUNNING" {
		t.Fatalf("status fallback = %q", j.status())
	}
	j.Result.Status = "SUCCESS"
	if j.status() != "SUCCESS" {
		t.Fatalf("status = %q", j.status())
	}
}
