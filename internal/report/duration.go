package report

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// isoDurationRe matches the PnYnMnWnDTnHnMnS subset of ISO-8601 durations that PPDM
// protection-policy objectives use (e.g. PT24H, P30D, P1W, PT12H30M). Every component
// is optional, so a follow-up check rejects the empty "P"/"PT" forms.
var isoDurationRe = regexp.MustCompile(
	`^P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseISODuration parses the ISO-8601 duration subset PPDM emits into a time.Duration.
// Months are treated as ~30 days and years as ~365 days: calendar-exact arithmetic is
// neither available nor meaningful for an SLA window, so these approximations are
// deliberate. Empty or malformed input returns an error.
func parseISODuration(s string) (time.Duration, error) {
	m := isoDurationRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid ISO-8601 duration %q", s)
	}
	const day = 24 * time.Hour
	units := []time.Duration{365 * day, 30 * day, 7 * day, day, time.Hour, time.Minute, time.Second}
	var total time.Duration
	matched := false
	for i, u := range units {
		g := m[i+1]
		if g == "" {
			continue
		}
		n, err := strconv.Atoi(g)
		if err != nil {
			return 0, fmt.Errorf("invalid ISO-8601 duration %q: %w", s, err)
		}
		total += time.Duration(n) * u
		matched = true
	}
	if !matched {
		return 0, fmt.Errorf("empty ISO-8601 duration %q", s)
	}
	return total, nil
}
