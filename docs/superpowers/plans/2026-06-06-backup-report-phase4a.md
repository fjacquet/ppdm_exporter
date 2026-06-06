# Backup Reporter Phase 4a — Scheduled Generation + Delivery — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A per-tenant, calendar-scheduled loop in the report process that builds each tenant's Phase-3 report and emails it (HTML body + PDF attachment) via SMTP, restart-safely and auditably.

**Architecture:** A new `schedule.Scheduler` loop (parallel to the capture loop) computes per-tenant due-ness from pure cadence functions, calls `render.Build`/`RenderHTML`/`RenderPDF`, delivers through a `delivery.Deliverer` (SMTP impl via go-mail), and records each send in a `report_deliveries` table that also dedupes. Cadence is presets (daily/weekly/monthly + hour, UTC).

**Tech Stack:** Go 1.26, pgx v5, `github.com/wneessen/go-mail` (report-binary-only), cobra, testcontainers-go, `axllent/mailpit` (demo SMTP sink).

Spec: `docs/superpowers/specs/2026-06-06-backup-report-phase4a-design.md`.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/config/report.go` (modify) | add `SMTP` + `Schedule` structs, `SMTP`/`Schedules` fields, `ParseWeekday`, interpolation + validation |
| `internal/config/report_test.go` (modify) | config parse/validation tests |
| `internal/report/migrations.sql` (modify) | `report_deliveries` table |
| `internal/report/store.go` (modify) | `DeliveryExists`, `RecordDelivery` |
| `internal/report/store_deliveries_test.go` (create) | testcontainers tests for the two methods |
| `internal/report/delivery/delivery.go` (create) | `Deliverer` interface |
| `internal/report/delivery/smtp.go` (create) | `SMTP` impl + `buildMessage` (go-mail) |
| `internal/report/delivery/smtp_test.go` (create) | `buildMessage` compose test (via `WriteTo`) |
| `internal/report/schedule/cadence.go` (create) | `PeriodKey`/`ScheduledTime`/`Due` (pure) |
| `internal/report/schedule/cadence_test.go` (create) | pure cadence unit tests |
| `internal/report/schedule/scheduler.go` (create) | `Scheduler` + `New`/`Run`/`runDue`/`maybeSend` |
| `internal/report/schedule/scheduler_test.go` (create) | scheduler tests (testcontainers + fake Deliverer) |
| `cmd/report/main.go` (modify) | start the scheduler when schedules configured |
| `go.mod`/`go.sum` (modify) | add go-mail |
| `docker-compose.yml`, `config.report.demo.yaml`, `Makefile`, `docs/report.md`, `CHANGELOG.md` (modify) | demo + docs |

**Conventions (from CLAUDE.md / prior phases):** parameterized SQL only; no inline lint/semgrep suppressions (restructure); `gofmt -w` before each commit; testcontainers wait on the second "ready to accept connections" log (use `reporttest.NewStore`); commit messages end with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. `internal/report/render` already provides `Build`, `RenderHTML`, `RenderPDF`, and `ReportData.BadgeText()`.

---

## Task 1: Config — `SMTP` + `Schedule`

**Files:**
- Modify: `internal/config/report.go`
- Test: `internal/config/report_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/report_test.go`:

```go
func TestLoadReportSchedules(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "smtps3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
smtp:
  host: smtp.example.com
  from: "assurance@example.com"
  username: smtpuser
  password: "${SMTP_PASSWORD}"
  starttls: true
schedules:
  - {tenant: acme, cadence: weekly, weekday: Mon, hour: 6, recipients: [ops@acme.com]}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.SMTP.Host != "smtp.example.com" || cfg.SMTP.Port != 587 || cfg.SMTP.Password != "smtps3cret" {
		t.Fatalf("smtp = %+v", cfg.SMTP)
	}
	if len(cfg.Schedules) != 1 {
		t.Fatalf("schedules = %d", len(cfg.Schedules))
	}
	s := cfg.Schedules[0]
	if s.Tenant != "acme" || s.Cadence != "weekly" || s.Weekday != "Mon" || s.Hour != 6 ||
		len(s.Recipients) != 1 {
		t.Fatalf("schedule = %+v", s)
	}
}

func TestLoadReportScheduleValidation(t *testing.T) {
	base := "database: {dsn: x}\nservers:\n  - {name: p, host: h, username: u, password: p}\n"
	cases := map[string]string{
		"bad cadence":   base + "smtp: {host: h, from: f}\nschedules:\n  - {tenant: a, cadence: hourly, hour: 6, recipients: [x@y.z]}\n",
		"bad hour":      base + "smtp: {host: h, from: f}\nschedules:\n  - {tenant: a, cadence: daily, hour: 99, recipients: [x@y.z]}\n",
		"no recipients": base + "smtp: {host: h, from: f}\nschedules:\n  - {tenant: a, cadence: daily, hour: 6, recipients: []}\n",
		"no smtp host":  base + "smtp: {from: f}\nschedules:\n  - {tenant: a, cadence: daily, hour: 6, recipients: [x@y.z]}\n",
		"bad weekday":   base + "smtp: {host: h, from: f}\nschedules:\n  - {tenant: a, cadence: weekly, weekday: Funday, hour: 6, recipients: [x@y.z]}\n",
	}
	for name, y := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "r.yaml")
		_ = os.WriteFile(path, []byte(y), 0o600)
		if _, err := LoadReport(path); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestParseWeekday(t *testing.T) {
	if d, err := ParseWeekday("Mon"); err != nil || d != time.Monday {
		t.Errorf("Mon -> %v,%v", d, err)
	}
	if _, err := ParseWeekday("xyz"); err == nil {
		t.Error("expected error for xyz")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestLoadReportSchedules|TestLoadReportScheduleValidation|TestParseWeekday' ./internal/config/`
Expected: build failure — `cfg.SMTP undefined`, `undefined: ParseWeekday`.

- [ ] **Step 3: Add the types, ParseWeekday, and validation to `report.go`**

Add structs (near `Compliance`/`ReportOutput`):

```go
// SMTP configures outbound email delivery for scheduled reports.
type SMTP struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"` // defaults to 587
	From     string `yaml:"from"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	StartTLS bool   `yaml:"starttls"`
}

// Schedule is one per-tenant report cadence. Times are UTC.
type Schedule struct {
	Tenant     string   `yaml:"tenant"`
	Cadence    string   `yaml:"cadence"` // daily | weekly | monthly
	Weekday    string   `yaml:"weekday"` // weekly only (Mon..Sun)
	Day        int      `yaml:"day"`     // monthly only (1..31, clamped to month length)
	Hour       int      `yaml:"hour"`    // 0..23
	Recipients []string `yaml:"recipients"`
}
```

Add the fields to `ReportConfig` (after `Report ReportOutput`):

```go
	Report     ReportOutput   `yaml:"report"`
	SMTP       SMTP           `yaml:"smtp"`
	Schedules  []Schedule     `yaml:"schedules"`
}
```

Add `ParseWeekday` (package-level):

```go
// ParseWeekday maps a 3-letter day abbreviation (case-insensitive) to a time.Weekday.
func ParseWeekday(s string) (time.Weekday, error) {
	switch strings.ToLower(s) {
	case "sun":
		return time.Sunday, nil
	case "mon":
		return time.Monday, nil
	case "tue":
		return time.Tuesday, nil
	case "wed":
		return time.Wednesday, nil
	case "thu":
		return time.Thursday, nil
	case "fri":
		return time.Friday, nil
	case "sat":
		return time.Saturday, nil
	default:
		return 0, fmt.Errorf("invalid weekday %q (want Mon..Sun)", s)
	}
}
```

In `LoadReport`, after the existing `report` interpolation/defaults block and before the final required-field checks, add interpolation + validation:

```go
	smtpUser, err := interpolate(cfg.SMTP.Username)
	if err != nil {
		return nil, fmt.Errorf("smtp username: %w", err)
	}
	cfg.SMTP.Username = smtpUser
	smtpPass, err := interpolate(cfg.SMTP.Password)
	if err != nil {
		return nil, fmt.Errorf("smtp password: %w", err)
	}
	cfg.SMTP.Password = smtpPass
	if len(cfg.Schedules) > 0 {
		if cfg.SMTP.Port == 0 {
			cfg.SMTP.Port = 587
		}
		if cfg.SMTP.Host == "" || cfg.SMTP.From == "" {
			return nil, fmt.Errorf("smtp.host and smtp.from are required when schedules are set")
		}
		for i, s := range cfg.Schedules {
			switch s.Cadence {
			case "daily", "weekly", "monthly":
			default:
				return nil, fmt.Errorf("schedule %d: invalid cadence %q (want daily|weekly|monthly)", i, s.Cadence)
			}
			if s.Hour < 0 || s.Hour > 23 {
				return nil, fmt.Errorf("schedule %d: hour %d out of range 0..23", i, s.Hour)
			}
			if len(s.Recipients) == 0 {
				return nil, fmt.Errorf("schedule %d (%s): recipients required", i, s.Tenant)
			}
			if s.Cadence == "weekly" {
				if _, err := ParseWeekday(s.Weekday); err != nil {
					return nil, fmt.Errorf("schedule %d: %w", i, err)
				}
			}
			if s.Cadence == "monthly" && (s.Day < 1 || s.Day > 31) {
				return nil, fmt.Errorf("schedule %d: day %d out of range 1..31", i, s.Day)
			}
		}
	}
```

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/config/ && go test ./internal/config/`
Expected: PASS (all config tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/report.go internal/config/report_test.go
git commit -m "$(printf 'feat(config): smtp + schedules blocks for report delivery\n\nSMTP (host/port/from/user/pass/starttls, env-interpolated) and per-tenant\nSchedule (cadence daily|weekly|monthly + weekday/day/hour, recipients), with\nParseWeekday and validation gated on schedules being set.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 2: Cadence — `PeriodKey` / `ScheduledTime` / `Due`

**Files:**
- Create: `internal/report/schedule/cadence.go`, `internal/report/schedule/cadence_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/report/schedule/cadence_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/report/schedule/`
Expected: build failure — `undefined: PeriodKey` / `Due`.

- [ ] **Step 3: Implement `cadence.go`**

Create `internal/report/schedule/cadence.go`:

```go
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
```

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/report/schedule/ && go test ./internal/report/schedule/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/schedule/cadence.go internal/report/schedule/cadence_test.go
git commit -m "$(printf 'feat(schedule): cadence functions (PeriodKey/ScheduledTime/Due)\n\nPure UTC cadence math for daily/weekly/monthly with hour/weekday/day, ISO-week\nkeys, month-day clamping, and catch-up (due = now >= occurrence send time).\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 3: Store — `report_deliveries` + `DeliveryExists` / `RecordDelivery`

**Files:**
- Modify: `internal/report/migrations.sql`, `internal/report/store.go`
- Test: `internal/report/store_deliveries_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/report/store_deliveries_test.go`:

```go
package report

import (
	"context"
	"testing"
)

func TestDeliveriesExistsAndRecord(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ok, err := st.DeliveryExists(ctx, "acme", "2026-W23")
	if err != nil || ok {
		t.Fatalf("initial exists = %v,%v want false,nil", ok, err)
	}
	// A failed delivery must NOT count as existing (so it retries).
	if err := st.RecordDelivery(ctx, "acme", "2026-W23", false, "smtp down", []string{"a@b.c"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-W23"); ok {
		t.Error("failed delivery should not count as existing")
	}
	// A later success for the same period upserts and now counts.
	if err := st.RecordDelivery(ctx, "acme", "2026-W23", true, "", []string{"a@b.c", "d@e.f"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-W23"); !ok {
		t.Error("successful delivery should count as existing")
	}
	// Different period is independent.
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-W24"); ok {
		t.Error("other period should not exist")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestDeliveriesExistsAndRecord ./internal/report/`
Expected: build failure — `st.DeliveryExists undefined`.

- [ ] **Step 3a: Add the table to `migrations.sql`**

Append to `internal/report/migrations.sql`:

```sql
-- Phase 4a: provenance + dedupe for scheduled report deliveries. One row per (tenant, period
-- occurrence); a failed attempt is overwritten by a later success, and DeliveryExists counts only
-- successes so failures retry until the occurrence's period rolls over.
CREATE TABLE IF NOT EXISTS report_deliveries (
  tenant text NOT NULL,
  period text NOT NULL,
  sent_at timestamptz NOT NULL DEFAULT now(),
  ok boolean NOT NULL,
  error text,
  recipients text,
  PRIMARY KEY (tenant, period)
);
```

- [ ] **Step 3b: Add the methods to `store.go`**

Add (import `strings` is already present in store.go? if not, add it):

```go
// DeliveryExists reports whether a SUCCESSFUL report delivery is already recorded for the
// (tenant, period) occurrence. Failed attempts return false so the scheduler retries them.
func (s *Store) DeliveryExists(ctx context.Context, tenant, period string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM report_deliveries WHERE tenant=$1 AND period=$2 AND ok)`,
		tenant, period).Scan(&exists)
	return exists, err
}

// RecordDelivery idempotently upserts the outcome of a delivery attempt for (tenant, period);
// a later success overwrites an earlier failure.
func (s *Store) RecordDelivery(ctx context.Context, tenant, period string, ok bool, errMsg string, recipients []string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO report_deliveries (tenant, period, sent_at, ok, error, recipients)
		 VALUES ($1,$2, now(), $3,$4,$5)
		 ON CONFLICT (tenant, period) DO UPDATE SET sent_at=now(), ok=EXCLUDED.ok,
		  error=EXCLUDED.error, recipients=EXCLUDED.recipients`,
		tenant, period, ok, errMsg, strings.Join(recipients, ","))
	return err
}
```

> If `store.go` does not already import `strings`, add it to the import block.

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/report/ && go test -run TestDeliveriesExistsAndRecord ./internal/report/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/report/migrations.sql internal/report/store.go internal/report/store_deliveries_test.go
git commit -m "$(printf 'feat(report): report_deliveries table + DeliveryExists/RecordDelivery\n\nProvenance + dedupe for scheduled deliveries. DeliveryExists counts only ok=true\nso failures retry; RecordDelivery upserts (tenant, period) so a success overwrites\nan earlier failure.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 4: Delivery — `Deliverer` interface + SMTP (go-mail)

**Files:**
- Modify: `go.mod`/`go.sum` (add go-mail)
- Create: `internal/report/delivery/delivery.go`, `internal/report/delivery/smtp.go`, `internal/report/delivery/smtp_test.go`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/wneessen/go-mail@latest`
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Write the failing test**

Create `internal/report/delivery/smtp_test.go`:

```go
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
	for _, want := range []string{"Report — acme [FAIL]", "ops@acme.com", "text/html", "application/pdf", "acme-report.pdf"} {
		if !strings.Contains(out, want) {
			t.Errorf("message missing %q", want)
		}
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/report/delivery/`
Expected: build failure — `undefined: buildMessage`.

- [ ] **Step 4a: Create the interface `delivery.go`**

```go
// Package delivery sends rendered reports to recipients. Email (SMTP) is the only channel today;
// the Deliverer interface keeps file/webhook channels drop-in.
package delivery

import "context"

// Deliverer sends one tenant's rendered report to its recipients.
type Deliverer interface {
	Deliver(ctx context.Context, tenant string, to []string, subject string, html, pdf []byte) error
}
```

- [ ] **Step 4b: Create `smtp.go`**

```go
package delivery

import (
	"bytes"
	"context"
	"fmt"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/wneessen/go-mail"
)

// SMTP delivers reports as email (HTML body + PDF attachment) via go-mail.
type SMTP struct {
	client *mail.Client
	from   string
}

// NewSMTP builds an SMTP deliverer from config. STARTTLS is mandatory when cfg.StartTLS is set
// (port 587 style); otherwise TLS is disabled (e.g. a local demo sink). Auth is enabled only when
// a username is configured.
func NewSMTP(cfg config.SMTP) (*SMTP, error) {
	opts := []mail.Option{mail.WithPort(cfg.Port)}
	if cfg.StartTLS {
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory))
	} else {
		opts = append(opts, mail.WithTLSPortPolicy(mail.NoTLS))
	}
	if cfg.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(cfg.Username),
			mail.WithPassword(cfg.Password),
		)
	}
	client, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("smtp client: %w", err)
	}
	return &SMTP{client: client, from: cfg.From}, nil
}

// Deliver composes and sends the email.
func (s *SMTP) Deliver(ctx context.Context, tenant string, to []string, subject string, html, pdf []byte) error {
	msg, err := buildMessage(s.from, subject, to, html, fmt.Sprintf("%s-report.pdf", tenant), pdf)
	if err != nil {
		return err
	}
	return s.client.DialAndSendWithContext(ctx, msg)
}

// buildMessage assembles a multipart email: a short plain-text body, an HTML alternative (the
// rendered report), and the PDF as an attachment. Factored out so it can be asserted without a
// live SMTP server.
func buildMessage(from, subject string, to []string, html []byte, pdfName string, pdf []byte) (*mail.Msg, error) {
	m := mail.NewMsg()
	if err := m.From(from); err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if err := m.To(to...); err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	m.Subject(subject)
	m.SetDate()
	m.SetMessageID()
	m.SetBodyString(mail.TypeTextPlain, "Your backup assurance report is attached; an HTML version is included in this message.")
	m.AddAlternativeString(mail.TypeTextHTML, string(html))
	if len(pdf) > 0 {
		if err := m.AttachReader(pdfName, bytes.NewReader(pdf)); err != nil {
			return nil, fmt.Errorf("attach pdf: %w", err)
		}
	}
	return m, nil
}
```

> If `go build` reports a different signature for `AttachReader` or the TLS-policy/auth constants, run `go doc github.com/wneessen/go-mail | grep -iE 'AttachReader|TLSPolicy|SMTPAuth|WithTLSPortPolicy'` and adjust to the installed version (the names above are from go-mail v0.6). `mail.NoTLS`, `mail.TLSMandatory`, `mail.SMTPAuthAutoDiscover`, `mail.TypeTextPlain/TypeTextHTML` are the expected identifiers.

- [ ] **Step 5: Run to verify passing**

Run: `gofmt -w internal/report/delivery/ && go test ./internal/report/delivery/`
Expected: PASS (`%PDF`, html, filename present in the composed message).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/report/delivery/
git commit -m "$(printf 'feat(delivery): Deliverer interface + SMTP email (go-mail)\n\nEmail report as HTML body + PDF attachment via go-mail; STARTTLS+auth when\nconfigured, no-TLS/no-auth for a local sink. buildMessage is compose-tested via\nWriteTo (no live server). go-mail is report-binary-only; exporter image untouched.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 5: Scheduler — loop + per-tenant generate/deliver/record

**Files:**
- Create: `internal/report/schedule/scheduler.go`, `internal/report/schedule/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/report/schedule/scheduler_test.go`:

```go
package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/reporttest"
)

type fakeDeliverer struct {
	calls   int
	failNext bool
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ string, _ []string, _ string, _, _ []byte) error {
	f.calls++
	if f.failNext {
		return errors.New("smtp boom")
	}
	return nil
}

// seedTenant gives "acme" one asset + a copy + a default target so render.Build succeeds.
func seedTenant(t *testing.T, st *report.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertSLATargets(ctx, []report.SLATarget{
		{Tenant: "acme", RPOSeconds: 86400, RetentionDays: 30, MinCopies: 2, GraceSeconds: 14400, Source: "default"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAssets(ctx, "acme", "s1", []report.Asset{
		{ID: "v1", Name: "vm", Type: "VMWARE_VIRTUAL_MACHINE", ProtectionStatus: "PROTECTED",
			LastAvailableCopyTime: now.Add(-time.Hour).Format(time.RFC3339)},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCopies(ctx, "acme", "s1", []report.Copy{
		{ID: "c1", AssetID: "v1", CreateTime: now.Add(-time.Hour).Format(time.RFC3339),
			RetentionTime: now.Add(30 * 24 * time.Hour).Format(time.RFC3339)},
	}, now); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerSendsOncePerPeriod(t *testing.T) {
	st := reporttest.NewStore(t)
	seedTenant(t, st)
	fd := &fakeDeliverer{}
	sc := New(st, fd, []config.Schedule{
		{Tenant: "acme", Cadence: "daily", Hour: 0, Recipients: []string{"ops@acme.com"}},
	}, "Acme Co")
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC) // past hour 0 -> due

	sc.runDue(ctx, now)
	if fd.calls != 1 {
		t.Fatalf("first tick calls = %d, want 1", fd.calls)
	}
	sc.runDue(ctx, now) // same period -> no resend
	if fd.calls != 1 {
		t.Fatalf("second tick calls = %d, want 1 (deduped)", fd.calls)
	}
	if ok, _ := st.DeliveryExists(ctx, "acme", "2026-06-06"); !ok {
		t.Error("expected recorded delivery")
	}
}

func TestSchedulerRetriesOnFailure(t *testing.T) {
	st := reporttest.NewStore(t)
	seedTenant(t, st)
	fd := &fakeDeliverer{failNext: true}
	sc := New(st, fd, []config.Schedule{
		{Tenant: "acme", Cadence: "daily", Hour: 0, Recipients: []string{"ops@acme.com"}},
	}, "Acme Co")
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)

	sc.runDue(ctx, now) // fails -> ok=false, no dedupe
	if fd.calls != 1 {
		t.Fatalf("calls = %d, want 1", fd.calls)
	}
	fd.failNext = false
	sc.runDue(ctx, now) // retries because the prior attempt failed
	if fd.calls != 2 {
		t.Fatalf("calls after retry = %d, want 2", fd.calls)
	}
}

func TestSchedulerSkipsTenantWithNoData(t *testing.T) {
	st := reporttest.NewStore(t)
	fd := &fakeDeliverer{}
	sc := New(st, fd, []config.Schedule{
		{Tenant: "ghost", Cadence: "daily", Hour: 0, Recipients: []string{"x@y.z"}},
	}, "B")
	sc.runDue(context.Background(), time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC))
	if fd.calls != 0 {
		t.Errorf("no-data tenant should not deliver; calls = %d", fd.calls)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run TestScheduler ./internal/report/schedule/`
Expected: build failure — `undefined: New` / `sc.runDue`.

- [ ] **Step 3: Implement `scheduler.go`**

```go
package schedule

import (
	"context"
	"bytes"
	"fmt"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/delivery"
	"github.com/fjacquet/ppdm_exporter/internal/report/render"
	log "github.com/sirupsen/logrus"
)

// Scheduler periodically generates and delivers each tenant's report on its cadence.
type Scheduler struct {
	store     *report.Store
	deliverer delivery.Deliverer
	schedules []config.Schedule
	brand     string
	tick      time.Duration
}

// New wires a scheduler. The tick (how often due-ness is re-checked) is 1 minute — fine for
// hour-granularity cadences.
func New(store *report.Store, d delivery.Deliverer, schedules []config.Schedule, brand string) *Scheduler {
	return &Scheduler{store: store, deliverer: d, schedules: schedules, brand: brand, tick: time.Minute}
}

// Run checks due-ness immediately, then every tick, until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	s.runDue(ctx, time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runDue(ctx, time.Now().UTC())
		}
	}
}

func (s *Scheduler) runDue(ctx context.Context, now time.Time) {
	for _, sc := range s.schedules {
		s.maybeSend(ctx, sc, now)
	}
}

// maybeSend delivers one tenant's report if it is due and not already delivered for the period.
// A panic, render error, or delivery error is contained to this tenant (logged + recorded) so it
// neither blocks other tenants nor kills the loop.
func (s *Scheduler) maybeSend(ctx context.Context, sc config.Schedule, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"tenant": sc.Tenant, "panic": r}).Error("schedule send panicked")
		}
	}()
	if !Due(now, sc) {
		return
	}
	period := PeriodKey(now, sc)
	exists, err := s.store.DeliveryExists(ctx, sc.Tenant, period)
	if err != nil {
		log.WithError(err).WithField("tenant", sc.Tenant).Warn("delivery-exists check failed")
		return
	}
	if exists {
		return
	}
	record := func(ok bool, msg string) {
		if rerr := s.store.RecordDelivery(ctx, sc.Tenant, period, ok, msg, sc.Recipients); rerr != nil {
			log.WithError(rerr).WithField("tenant", sc.Tenant).Warn("record delivery failed")
		}
	}
	data, err := render.Build(ctx, s.store, sc.Tenant, s.brand, now)
	if err != nil {
		log.WithError(err).WithField("tenant", sc.Tenant).Info("skip delivery: no report data")
		record(false, err.Error())
		return
	}
	var html, pdf bytes.Buffer
	if err := render.RenderHTML(&html, data); err != nil {
		record(false, "render html: "+err.Error())
		return
	}
	if err := render.RenderPDF(&pdf, data); err != nil {
		record(false, "render pdf: "+err.Error())
		return
	}
	subject := fmt.Sprintf("Backup Assurance Report — %s — %s [%s]", sc.Tenant, period, data.BadgeText())
	derr := s.deliverer.Deliver(ctx, sc.Tenant, sc.Recipients, subject, html.Bytes(), pdf.Bytes())
	if derr != nil {
		log.WithError(derr).WithField("tenant", sc.Tenant).Warn("report delivery failed")
		record(false, derr.Error())
		return
	}
	log.WithFields(log.Fields{"tenant": sc.Tenant, "period": period}).Info("report delivered")
	record(true, "")
}
```

- [ ] **Step 4: Run to verify passing**

Run: `gofmt -w internal/report/schedule/ && go test ./internal/report/schedule/`
Expected: PASS (sends once, dedupes, retries on failure, skips no-data tenant).

- [ ] **Step 5: Commit**

```bash
git add internal/report/schedule/scheduler.go internal/report/schedule/scheduler_test.go
git commit -m "$(printf 'feat(schedule): Scheduler loop — generate, deliver, record\n\nPer tick, for each due+undelivered tenant: render.Build -> HTML+PDF -> Deliverer\n-> RecordDelivery. Per-tenant panic/render/delivery errors are contained and\nrecorded (failures retry next tick); no-data tenants are skipped.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 6: Wire the scheduler into `cmd/report`

**Files:**
- Modify: `cmd/report/main.go`

- [ ] **Step 1: Add imports + wiring**

Add imports `"github.com/fjacquet/ppdm_exporter/internal/report/delivery"` and
`"github.com/fjacquet/ppdm_exporter/internal/report/schedule"`. In `run(...)`, after the HTTP-server
block and before building `servers`, add:

```go
	if len(cfg.Schedules) > 0 {
		deliverer, derr := delivery.NewSMTP(cfg.SMTP)
		if derr != nil {
			return derr
		}
		sched := schedule.New(store, deliverer, cfg.Schedules, cfg.Report.BrandName)
		go sched.Run(ctx)
		log.WithField("schedules", len(cfg.Schedules)).Info("report scheduler started")
	}
```

- [ ] **Step 2: Build to verify**

Run: `gofmt -w cmd/report/ && go build ./... && go vet ./...`
Expected: clean build and vet.

- [ ] **Step 3: Commit**

```bash
git add cmd/report/main.go
git commit -m "$(printf 'feat(report): start the scheduler when schedules are configured\n\nThe capture process builds the SMTP deliverer and runs schedule.Scheduler in a\ngoroutine when cfg.Schedules is non-empty (mirrors the HTTP endpoint wiring);\nempty schedules leave the process unchanged.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 7: Demo (mailpit) + docs + changelog

**Files:**
- Modify: `docker-compose.yml`, `config.report.demo.yaml`, `Makefile`, `docs/report.md`, `CHANGELOG.md`

- [ ] **Step 1: Add a mailpit service to `docker-compose.yml`**

Add a service (and make `report` depend on it):

```yaml
  mailpit:
    image: axllent/mailpit:latest
    container_name: ppdm_mailpit
    ports:
      - "8025:8025"   # web UI; SMTP on 1025 is reachable in-network as mailpit:1025
    restart: unless-stopped
```

In the `report` service `depends_on`, add `mailpit: {condition: service_started}`.

- [ ] **Step 2: Add smtp + schedules to `config.report.demo.yaml`**

Append:

```yaml
smtp:
  host: mailpit
  port: 1025
  from: "assurance@demo.local"
  starttls: false        # demo sink: no TLS/auth (Mailpit). NEVER do this in production.
schedules:
  - tenant: acme-corp
    cadence: daily
    hour: 0              # 00:00 UTC -> always "due" in the demo, sends on first tick
    recipients: [ops@acme-corp.demo]
```

- [ ] **Step 3: Add the Mailpit URL to the `make demo` banner**

In the `demo:` recipe banner (after the Report lines), add:

```makefile
	@echo "  Mailpit      http://localhost:8025  (scheduled reports land here)"
```

- [ ] **Step 4: Document in `docs/report.md`**

Append:

````markdown
## Scheduled delivery (Phase 4a)

Configure SMTP and per-tenant schedules; the report process emails each tenant's report
(HTML body + PDF attachment) on its cadence (daily/weekly/monthly + hour, UTC):

```yaml
smtp: {host: smtp.example.com, port: 587, from: assurance@example.com,
       username: "${SMTP_USER}", password: "${SMTP_PASSWORD}", starttls: true}
schedules:
  - {tenant: acme-corp, cadence: weekly, weekday: Mon, hour: 6, recipients: [ops@acme.com]}
```

Deliveries are recorded in `report_deliveries` (one row per tenant+occurrence); a restart near
the send time won't re-send, and a failed send retries on the next minute-tick until it succeeds.
In the demo, sent mail appears in **Mailpit** at `http://localhost:8025`.
````

- [ ] **Step 5: CHANGELOG entry**

Under `## [Unreleased]` → `### Added` in `CHANGELOG.md`:

```markdown
- `cmd/report` Phase 4a: scheduled per-tenant report delivery — an in-process scheduler
  (daily/weekly/monthly + hour, UTC) emails each tenant's report (HTML + PDF attachment) over
  SMTP, deduped/audited via `report_deliveries`. Demo adds a Mailpit sink.
```

- [ ] **Step 6: Validate + commit**

Run: `python3 -c "import yaml" 2>/dev/null && python3 -c "import yaml;yaml.safe_load(open('config.report.demo.yaml'))" || ruby -ryaml -e 'YAML.safe_load(File.read("config.report.demo.yaml"))'` (expect no error); `docker compose config -q`.

```bash
git add docker-compose.yml config.report.demo.yaml Makefile docs/report.md CHANGELOG.md
git commit -m "$(printf 'docs(report): Phase 4a demo (mailpit) + scheduled-delivery docs\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Task 8: Gate + security + push

- [ ] **Step 1: Full CI gate**

Run: `make ci`
Expected: gofmt/vet clean, golangci-lint `0 issues`, `go test -race ./...` pass, govulncheck `No vulnerabilities found` (if go-mail or a transitive dep flags an advisory, bump it like the Phase 2 x/crypto fix), build OK.

- [ ] **Step 2: Semgrep**

Scan the new Go files (via the semgrep MCP tool or `semgrep --config auto`): `internal/report/delivery/*.go`,
`internal/report/schedule/*.go`, `internal/report/store.go`, `internal/config/report.go`,
`cmd/report/main.go`. Expect 0 findings; no inline suppressions.

- [ ] **Step 3: Manual end-to-end (optional, needs Docker)**

Run: `make demo`, wait ~1 minute, then open `http://localhost:8025` — the acme-corp report email with a
PDF attachment should be present. `SELECT tenant, period, ok FROM report_deliveries;` shows one `ok=true`
row; it does not grow on subsequent ticks.

- [ ] **Step 4: Security review**

Phase 4a adds outbound SMTP with credentials. Run `/security-review` on the branch (focus: SMTP TLS/auth
posture, credential handling from `${ENV}`, no secret logging, recipient handling). Address high-confidence findings.

- [ ] **Step 5: Push**

```bash
git push -u origin feat/backup-report-phase4a
```

---

## Self-review notes (spec coverage)

- Spec §1 scheduler/cadence → Tasks 2 (cadence) + 5 (loop). §2 Deliverer/SMTP → Task 4. §3 store table+methods → Task 3. §4 config → Task 1. §5 wiring → Task 6. §6 error handling (retry on failure via ok-only DeliveryExists, per-tenant containment, ErrNoData skip) → Tasks 3 + 5. §7 testing → tests in each task + Task 8 gate. §8 demo → Task 7.
- Type/name consistency: `config.SMTP`/`config.Schedule`/`config.ParseWeekday`; `schedule.PeriodKey/ScheduledTime/Due/New/Run`; `delivery.Deliverer`/`delivery.NewSMTP`/`buildMessage`; `report.DeliveryExists`/`RecordDelivery`; `render.Build/RenderHTML/RenderPDF/ReportData.BadgeText` (existing).
- Deviation from spec §7 noted: the SMTP test is **compose-only** (`msg.WriteTo` assertions) rather than an in-process SMTP listener — true end-to-end send is exercised by the Mailpit demo (Task 7/8), avoiding a test-only SMTP server dependency.
