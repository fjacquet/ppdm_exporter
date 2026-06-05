package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInterpolatesEnvAndDefaults(t *testing.T) {
	t.Setenv("PPDM01_PASSWORD", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server: {host: "0.0.0.0", port: "9102", uri: "/metrics"}
collection: {interval: "5m", timeout: "60s", lookback: "24h"}
servers:
  - {name: ppdm01, host: ppdm01.example.com, username: u, password: "${PPDM01_PASSWORD}", insecureSkipVerify: true}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Servers[0].Password != "s3cret" {
		t.Fatalf("password = %q, want s3cret", cfg.Servers[0].Password)
	}
	if cfg.Servers[0].BaseURL() != "https://ppdm01.example.com:8443" {
		t.Fatalf("BaseURL = %q, want :8443 default", cfg.Servers[0].BaseURL())
	}
	if cfg.Collection.Lookback.String() != "24h0m0s" {
		t.Fatalf("lookback = %s, want 24h0m0s", cfg.Collection.Lookback)
	}
	if cfg.Collection.AssetAgeThreshold.String() != "24h0m0s" {
		t.Fatalf("assetAgeThreshold default = %s, want 24h0m0s", cfg.Collection.AssetAgeThreshold)
	}
}

func TestLoadRejectsEmptyServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(path, []byte("servers: []\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when no servers configured")
	}
}

func TestLoadFailsOnUnsetEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	yaml := `
servers:
  - {name: ppdm01, host: h, username: u, password: "${PPDM_NOPE_UNSET}"}
`
	_ = os.WriteFile(path, []byte(yaml), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unset env var reference")
	}
}
