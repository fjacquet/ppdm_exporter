// Package schedule computes per-tenant report due-ness (pure cadence functions) and runs the
// scheduled generate+deliver loop.
package schedule

import (
	"fmt"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
)

// PeriodKey identifies the occurrence of now under s's cadence: a delivery is recorded once per
// (tenant, period), so the key must be stable within an occurrence and change across them.
func PeriodKey(now time.Time, s config.Schedule) string {
	switch s.Cadence {
	case "weekly":
		y, w := now.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case "monthly":
		return now.Format("2006-01")
	default: // daily
		return now.Format("2006-01-02")
	}
}

// ScheduledTime is the send instant for the occurrence containing now (UTC): daily → today @ hour;
// weekly → this ISO week's weekday @ hour; monthly → this month's day (clamped) @ hour.
func ScheduledTime(now time.Time, s config.Schedule) time.Time {
	switch s.Cadence {
	case "weekly":
		wd, err := config.ParseWeekday(s.Weekday)
		if err != nil {
			wd = time.Monday
		}
		// Monday of now's week, then offset to the target weekday (ISO week starts Monday).
		offsetFromMonday := (int(now.Weekday()) + 6) % 7
		monday := time.Date(now.Year(), now.Month(), now.Day()-offsetFromMonday, 0, 0, 0, 0, time.UTC)
		target := monday.AddDate(0, 0, (int(wd)+6)%7)
		return time.Date(target.Year(), target.Month(), target.Day(), s.Hour, 0, 0, 0, time.UTC)
	case "monthly":
		day := s.Day
		if last := daysInMonth(now.Year(), now.Month()); day > last {
			day = last
		}
		return time.Date(now.Year(), now.Month(), day, s.Hour, 0, 0, 0, time.UTC)
	default: // daily
		return time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, time.UTC)
	}
}

// Due reports whether now is at or past the scheduled time for its occurrence.
func Due(now time.Time, s config.Schedule) bool {
	return !now.Before(ScheduledTime(now, s))
}

func daysInMonth(year int, m time.Month) int {
	// Day 0 of the next month is the last day of month m.
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
