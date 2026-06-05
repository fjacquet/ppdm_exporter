package config

import (
	"os"
	"path/filepath"
	"testing"
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

func TestLoadReportRejectsNoServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(path, []byte("database: {dsn: x}\nservers: []\n"), 0o600)
	if _, err := LoadReport(path); err == nil {
		t.Fatal("expected error for no servers")
	}
}
