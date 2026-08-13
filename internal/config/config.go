// Package config loads and validates the exporter configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Server is one PPDM server to monitor.
type Server struct {
	Name               string  `yaml:"name"`
	Host               string  `yaml:"host"`
	Port               int     `yaml:"port"` // defaults to 8443
	Username           string  `yaml:"username"`
	Password           string  `yaml:"password"`
	PasswordFile       string  `yaml:"passwordFile"`
	InsecureSkipVerify EnvBool `yaml:"insecureSkipVerify"`
}

// EnvBool is a boolean config value that may be written in YAML either as a native
// boolean (insecureSkipVerify: true) or as a ${VAR} environment reference resolved at
// secret-resolution time (insecureSkipVerify: ${PPDM1_SKIP_CERTIFICATE}). Backward
// compatible with existing native-bool configs; the zero value resolves to false.
type EnvBool struct {
	raw string // ${...} reference or literal string form, when written as a string
	val bool   // resolved value
}

// NewEnvBool returns an already-resolved EnvBool (for tests / programmatic config).
func NewEnvBool(v bool) EnvBool { return EnvBool{val: v} }

// Bool returns the resolved boolean value.
func (b EnvBool) Bool() bool { return b.val }

// UnmarshalYAML accepts either a native YAML boolean or a string (which may be a
// ${VAR} reference resolved later by Resolve).
func (b *EnvBool) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var bv bool
	if err := unmarshal(&bv); err == nil {
		b.val = bv
		return nil
	}
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("insecureSkipVerify must be a boolean or ${ENV} reference: %w", err)
	}
	b.raw = s
	return nil
}

// Resolve expands any ${VAR} reference (via expand) and parses the result to a bool.
// No-op when the value was a native boolean or omitted. An expansion that resolves to
// an empty string yields false; a non-boolean expansion is an error.
func (b *EnvBool) Resolve(expand func(string) (string, error)) error {
	if b.raw == "" {
		return nil
	}
	s, err := expand(b.raw)
	if err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		b.val = false
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("insecureSkipVerify: cannot parse %q as boolean", s)
	}
	b.val = v
	return nil
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
// AssetAgeThreshold gates per-asset last-copy-age emission; PerJobActivities opts into
// per-job activity metrics (higher cardinality — one series set per job in the window).
type Collection struct {
	Interval          time.Duration `yaml:"interval"`
	Timeout           time.Duration `yaml:"timeout"`
	Lookback          time.Duration `yaml:"lookback"`
	AssetAgeThreshold time.Duration `yaml:"assetAgeThreshold"`
	PerJobActivities  bool          `yaml:"perJobActivities"`
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

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// interpolate replaces every ${VAR} in s with its environment value, returning an
// error if any referenced variable is unset. Failing fast turns a typo'd secret
// name into a config-load error instead of repeated runtime auth failures.
//
// A reference may carry a fallback as ${VAR:-default}, borrowing the shell /
// docker-compose syntax and its meaning: unset OR empty falls back, and the reference
// never errors. That lets a shipped config.yaml drive a non-secret setting from the
// environment while still starting on a host that never exported it. Use it only where a
// safe default exists — a bare ${VAR} keeps the fail-loud behaviour that protects secrets.
func interpolate(s string) (string, error) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		name, fallback := sub[1], sub[2]
		v, ok := os.LookupEnv(name)
		if ok && v != "" {
			return v
		}
		if fallback != "" {
			return fallback[len(":-"):] // group 2 keeps its ":-" prefix, so "" means absent
		}
		if !ok {
			missing = append(missing, name)
		}
		return ""
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
		host, err := interpolate(s.Host)
		if err != nil {
			return nil, fmt.Errorf("server %s host: %w", s.Name, err)
		}
		s.Host = host
		username, err := interpolate(s.Username)
		if err != nil {
			return nil, fmt.Errorf("server %s username: %w", s.Name, err)
		}
		s.Username = username
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
		if err := s.InsecureSkipVerify.Resolve(interpolate); err != nil {
			return nil, fmt.Errorf("server %s insecureSkipVerify: %w", s.Name, err)
		}
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = "9442"
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
