// Package config loads and validates the exporter configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Server is one PPDM server to monitor.
type Server struct {
	Name               string `yaml:"name"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"` // defaults to 8443
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"passwordFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// BaseURL returns the https://host:port root for the PPDM REST API.
func (s Server) BaseURL() string {
	port := s.Port
	if port == 0 {
		port = 8443
	}
	return fmt.Sprintf("https://%s:%d", s.Host, port)
}

// ServerHTTP holds the exporter's own HTTP-server settings. Named to avoid colliding
// with the PPDM target Server struct.
type ServerHTTP struct {
	Host    string `yaml:"host"`
	Port    string `yaml:"port"`
	URI     string `yaml:"uri"`
	LogName string `yaml:"logName"`
}

// Collection holds loop timing. Lookback bounds the activities query window;
// AssetAgeThreshold gates per-asset last-copy-age emission.
type Collection struct {
	Interval          time.Duration `yaml:"interval"`
	Timeout           time.Duration `yaml:"timeout"`
	Lookback          time.Duration `yaml:"lookback"`
	AssetAgeThreshold time.Duration `yaml:"assetAgeThreshold"`
}

// OTel configures optional OTLP metric/trace export.
type OTel struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
	Interval string `yaml:"interval"`
}

// Config is the whole file.
type Config struct {
	Server     ServerHTTP `yaml:"server"`
	Collection Collection `yaml:"collection"`
	OTel       OTel       `yaml:"otel"`
	Servers    []Server   `yaml:"servers"`
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolate replaces every ${VAR} in s with its environment value, returning an
// error if any referenced variable is unset. Failing fast turns a typo'd secret
// name into a config-load error instead of repeated runtime auth failures.
func interpolate(s string) (string, error) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unset environment variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// Load reads, interpolates ${ENV} references, applies defaults, and validates.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		pw, err := interpolate(s.Password)
		if err != nil {
			return nil, fmt.Errorf("server %s password: %w", s.Name, err)
		}
		s.Password = pw
		if s.PasswordFile != "" && s.Password == "" {
			b, err := os.ReadFile(s.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("server %s passwordFile: %w", s.Name, err)
			}
			s.Password = strings.TrimSpace(string(b))
		}
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = "9102"
	}
	if cfg.Server.URI == "" {
		cfg.Server.URI = "/metrics"
	}
	if cfg.Collection.Interval == 0 {
		cfg.Collection.Interval = 5 * time.Minute
	}
	if cfg.Collection.Timeout == 0 {
		cfg.Collection.Timeout = 60 * time.Second
	}
	if cfg.Collection.Lookback == 0 {
		cfg.Collection.Lookback = 24 * time.Hour
	}
	if cfg.Collection.AssetAgeThreshold == 0 {
		cfg.Collection.AssetAgeThreshold = 24 * time.Hour
	}
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers configured")
	}
	return &cfg, nil
}
