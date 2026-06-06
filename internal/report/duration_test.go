package report

import (
	"testing"
	"time"
)

func TestParseISODuration(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"PT24H", 24 * time.Hour},
		{"P30D", 30 * day},
		{"P1W", 7 * day},
		{"PT12H30M", 12*time.Hour + 30*time.Minute},
		{"P1M", 30 * day},      // months are an explicit ~30d approximation
		{"P1Y", 365 * day},     // years are an explicit ~365d approximation
		{"PT45S", 45 * time.Second},
		{"P1DT2H", day + 2*time.Hour},
	}
	for _, c := range cases {
		got, err := parseISODuration(c.in)
		if err != nil {
			t.Errorf("parseISODuration(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseISODuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseISODurationInvalid(t *testing.T) {
	for _, in := range []string{"", "abc", "P", "PT", "30D", "P30", "PTH"} {
		if _, err := parseISODuration(in); err == nil {
			t.Errorf("parseISODuration(%q) expected error, got nil", in)
		}
	}
}
