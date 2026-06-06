package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadReportInterpolatesAndDefaults(t *testing.T) {
	t.Setenv("PG_PASSWORD", "pgsecret")
	t.Setenv("PPDM01_PASSWORD", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")
	yaml := `
database: {dsn: "postgres://u:${PG_PASSWORD}@localhost:5432/backup_report?sslmode=disable"}
capture: {interval: "1h", timeout: "5m", retentionDays: 400}
servers:
  - {name: ppdm01, tenant: acme, host: h, username: u, password: "${PPDM01_PASSWORD}", insecureSkipVerify: true}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Database.DSN != "postgres://u:pgsecret@localhost:5432/backup_report?sslmode=disable" {
		t.Fatalf("dsn = %q", cfg.Database.DSN)
	}
	if cfg.Servers[0].Tenant != "acme" || cfg.Servers[0].Password != "s3cret" {
		t.Fatalf("server = %+v", cfg.Servers[0])
	}
	if cfg.Capture.RetentionDays != 400 || cfg.Capture.Interval.String() != "1h0m0s" {
		t.Fatalf("capture = %+v", cfg.Capture)
	}
}

func TestLoadReportComplianceBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
compliance:
  grace: "4h"
  defaults: {rpoHours: 24, retentionDays: 30, minCopies: 2}
  overrides:
    - {tenant: acme-corp, assetType: VMWARE_VIRTUAL_MACHINE, policyName: "", rpoHours: 12, minCopies: 3}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	c := cfg.Compliance
	if c.Grace != 4*time.Hour {
		t.Errorf("grace = %v, want 4h", c.Grace)
	}
	if c.Defaults.RPOHours != 24 || c.Defaults.RetentionDays != 30 || c.Defaults.MinCopies != 2 {
		t.Errorf("defaults = %+v", c.Defaults)
	}
	if len(c.Overrides) != 1 {
		t.Fatalf("overrides = %d, want 1", len(c.Overrides))
	}
	o := c.Overrides[0]
	if o.Tenant != "acme-corp" || o.AssetType != "VMWARE_VIRTUAL_MACHINE" || o.PolicyName != "" ||
		o.RPOHours != 12 || o.MinCopies != 3 {
		t.Errorf("override = %+v", o)
	}
}

func TestLoadReportComplianceDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")
	// No compliance block at all — defaults must still be populated.
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	c := cfg.Compliance
	if c.Grace != 4*time.Hour {
		t.Errorf("default grace = %v, want 4h", c.Grace)
	}
	if c.Defaults.RPOHours != 24 || c.Defaults.RetentionDays != 30 || c.Defaults.MinCopies != 2 {
		t.Errorf("default compliance defaults = %+v", c.Defaults)
	}
}

func TestLoadReportRejectsNoServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(path, []byte("database: {dsn: x}\nservers: []\n"), 0o600)
	if _, err := LoadReport(path); err == nil {
		t.Fatal("expected error for no servers")
	}
}

func TestLoadReportReportBlock(t *testing.T) {
	t.Setenv("REPORT_TOKEN", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	yaml := `
database: {dsn: "postgres://u@localhost/db"}
servers:
  - {name: ppdm01, host: h, username: u, password: p}
report:
  listen: "127.0.0.1:9103"
  authToken: "${REPORT_TOKEN}"
  brandName: "Acme Backup Assurance"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Report.Listen != "127.0.0.1:9103" || cfg.Report.AuthToken != "s3cret" ||
		cfg.Report.BrandName != "Acme Backup Assurance" {
		t.Fatalf("report = %+v", cfg.Report)
	}
}

func TestLoadReportReportBrandDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(path, []byte("database: {dsn: x}\nservers:\n  - {name: p, host: h, username: u, password: p}\n"), 0o600)
	cfg, err := LoadReport(path)
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if cfg.Report.BrandName != "Backup Assurance Report" {
		t.Fatalf("default brand = %q", cfg.Report.BrandName)
	}
}

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
