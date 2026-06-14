package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherEmitsReloadedConfig(t *testing.T) {
	t.Setenv("PPDM01_PASSWORD", "p")
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	write := func(port string) {
		_ = os.WriteFile(path, []byte(
			"server: {port: \""+port+"\"}\ncollection: {interval: 5m}\n"+
				"servers:\n  - {name: ppdm01, host: h, username: u, password: \"${PPDM01_PASSWORD}\"}\n"), 0o600)
	}
	write("9442")

	w, err := NewWatcher(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	write("9103")
	w.Trigger() // simulate SIGHUP without sending a real signal

	select {
	case cfg := <-w.Updates():
		if cfg.Server.Port != "9103" {
			t.Fatalf("reloaded port = %s, want 9103", cfg.Server.Port)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no config update received")
	}
}
