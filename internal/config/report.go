package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// ReportServer is one PPDM server captured for backup history, tagged with a tenant.
type ReportServer struct {
	Name               string `yaml:"name"`
	Tenant             string `yaml:"tenant"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"` // defaults to 8443
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"passwordFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// BaseURL returns the https://host:port root for the PPDM REST API.
func (s ReportServer) BaseURL() string {
	port := s.Port
	if port == 0 {
		port = 8443
	}
	return fmt.Sprintf("https://%s:%d", s.Host, port)
}

// ComplianceTarget is an SLA target spec: backup-frequency (RPO), retention, and copy count.
type ComplianceTarget struct {
	RPOHours      int `yaml:"rpoHours"`
	RetentionDays int `yaml:"retentionDays"`
	MinCopies     int `yaml:"minCopies"`
}

// ComplianceOverride narrows a target to the assets it selects. Empty selector fields match
// any value; the most specific matching override wins (resolved in internal/report).
type ComplianceOverride struct {
	Tenant           string `yaml:"tenant"`
	AssetType        string `yaml:"assetType"`
	PolicyName       string `yaml:"policyName"`
	ComplianceTarget `yaml:",inline"`
}

// Compliance configures Phase 2 SLA target resolution: a lateness grace window, the
// per-tenant default target, and override rules that refine it.
type Compliance struct {
	Grace     time.Duration        `yaml:"grace"`
	Defaults  ComplianceTarget     `yaml:"defaults"`
	Overrides []ComplianceOverride `yaml:"overrides"`
}

// ReportOutput configures Phase 3 report generation: the optional HTTP endpoint and branding.
type ReportOutput struct {
	Listen    string `yaml:"listen"`    // empty = CLI-only (no HTTP endpoint)
	AuthToken string `yaml:"authToken"` // optional bearer; empty = no auth (localhost posture)
	BrandName string `yaml:"brandName"`
}

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

// ReportConfig is the cmd/report configuration.
type ReportConfig struct {
	Database struct {
		DSN string `yaml:"dsn"`
	} `yaml:"database"`
	Capture struct {
		Interval      time.Duration `yaml:"interval"`
		Timeout       time.Duration `yaml:"timeout"`
		RetentionDays int           `yaml:"retentionDays"`
	} `yaml:"capture"`
	Servers    []ReportServer `yaml:"servers"`
	Compliance Compliance     `yaml:"compliance"`
	Report     ReportOutput   `yaml:"report"`
	SMTP       SMTP           `yaml:"smtp"`
	Schedules  []Schedule     `yaml:"schedules"`
}

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

// LoadReport reads, interpolates ${ENV} references, applies defaults, and validates.
func LoadReport(path string) (*ReportConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ReportConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse report config: %w", err)
	}
	dsn, err := interpolate(cfg.Database.DSN)
	if err != nil {
		return nil, fmt.Errorf("database dsn: %w", err)
	}
	cfg.Database.DSN = dsn
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
		if s.Tenant == "" {
			s.Tenant = s.Name
		}
	}
	if cfg.Capture.Interval == 0 {
		cfg.Capture.Interval = time.Hour
	}
	if cfg.Capture.Timeout == 0 {
		cfg.Capture.Timeout = 5 * time.Minute
	}
	if cfg.Capture.RetentionDays == 0 {
		cfg.Capture.RetentionDays = 400
	}
	if cfg.Compliance.Grace == 0 {
		cfg.Compliance.Grace = 4 * time.Hour
	}
	if cfg.Compliance.Defaults.RPOHours == 0 {
		cfg.Compliance.Defaults.RPOHours = 24
	}
	if cfg.Compliance.Defaults.RetentionDays == 0 {
		cfg.Compliance.Defaults.RetentionDays = 30
	}
	if cfg.Compliance.Defaults.MinCopies == 0 {
		cfg.Compliance.Defaults.MinCopies = 2
	}
	token, err := interpolate(cfg.Report.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("report authToken: %w", err)
	}
	cfg.Report.AuthToken = token
	if cfg.Report.BrandName == "" {
		cfg.Report.BrandName = "Backup Assurance Report"
	}
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
	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("database.dsn is required")
	}
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers configured")
	}
	return &cfg, nil
}
