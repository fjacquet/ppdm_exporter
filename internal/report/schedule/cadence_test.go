package schedule

import (
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
)

func utc(y int, mo time.Month, d, h int) time.Time {
	return time.Date(y, mo, d, h, 0, 0, 0, time.UTC)
}

func TestPeriodKey(t *testing.T) {
	now := utc(2026, 6, 6, 12) // 2026-06-06 is a Saturday, ISO week 23
	cases := []struct {
		cadence, want string
	}{
		{"daily", "2026-06-06"},
		{"weekly", "2026-W23"},
		{"monthly", "2026-06"},
	}
	for _, c := range cases {
		got := PeriodKey(now, config.Schedule{Cadence: c.cadence})
		if got != c.want {
			t.Errorf("PeriodKey(%s) = %q, want %q", c.cadence, got, c.want)
		}
	}
}

func TestDueDaily(t *testing.T) {
	s := config.Schedule{Cadence: "daily", Hour: 6}
	if Due(utc(2026, 6, 6, 5), s) {
		t.Error("05:00 should not be due for hour 6")
	}
	if !Due(utc(2026, 6, 6, 6), s) {
		t.Error("06:00 should be due")
	}
	if !Due(utc(2026, 6, 6, 23), s) {
		t.Error("23:00 (past send hour, same day) should be due — catch-up")
	}
}

func TestDueWeekly(t *testing.T) {
	s := config.Schedule{Cadence: "weekly", Weekday: "Mon", Hour: 6}
	// 2026-06-01 is a Monday.
	if Due(utc(2026, 6, 1, 5), s) {
		t.Error("Mon 05:00 not due")
	}
	if !Due(utc(2026, 6, 1, 6), s) {
		t.Error("Mon 06:00 due")
	}
	if !Due(utc(2026, 6, 3, 0), s) {
		t.Error("Wed (past Mon in same week) should be due — catch-up")
	}
}

func TestDueMonthlyClampsDay(t *testing.T) {
	s := config.Schedule{Cadence: "monthly", Day: 31, Hour: 0}
	// February has 28 days in 2026; day 31 clamps to Feb 28.
	if Due(utc(2026, 2, 27, 12), s) {
		t.Error("Feb 27 should not be due when clamped target is Feb 28")
	}
	if !Due(utc(2026, 2, 28, 0), s) {
		t.Error("Feb 28 00:00 should be due (clamped from 31)")
	}
}
