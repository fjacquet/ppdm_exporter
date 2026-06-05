package ppdmclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

// Config configures a ServerClient. HTTPClient is optional (tests inject the
// httptest TLS client); when nil a client honoring InsecureSkipVerify is built.
type Config struct {
	Name               string
	BaseURL            string // e.g. https://ppdm01.example.com:8443
	Username           string
	Password           string
	InsecureSkipVerify bool
	HTTPClient         *http.Client
}

// ServerClient is the live per-server PPDM REST client.
type ServerClient struct {
	cfg Config
	rc  *resty.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewServerClient builds a client. Auth is lazy (on first Get).
func NewServerClient(cfg Config) *ServerClient {
	rc := resty.New().SetBaseURL(cfg.BaseURL)
	if cfg.HTTPClient != nil {
		rc.SetTransport(cfg.HTTPClient.Transport)
	} else {
		rc.SetTLSClientConfig(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // operator opt-in
		})
	}
	// Retry on 5xx only — never retry 4xx (auth/permission failures must not loop).
	rc.SetRetryCount(2).AddRetryCondition(func(r *resty.Response, _ error) bool {
		return r.StatusCode() >= 500
	})
	return &ServerClient{cfg: cfg, rc: rc}
}

func (c *ServerClient) Name() string { return c.cfg.Name }

// Get fetches path, authenticating first and re-authenticating once on 401.
func (c *ServerClient) Get(ctx context.Context, path string, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	resp, err := c.do(ctx, path, out)
	if err != nil {
		return err
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		c.clearToken()
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		if resp, err = c.do(ctx, path, out); err != nil {
			return err
		}
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode())
	}
	return nil
}

func (c *ServerClient) do(ctx context.Context, path string, out any) (*resty.Response, error) {
	return c.rc.R().SetContext(ctx).
		SetHeader("Authorization", "Bearer "+c.currentToken()).
		SetResult(out).
		Get(path)
}

func (c *ServerClient) currentToken() string { c.mu.Lock(); defer c.mu.Unlock(); return c.token }
func (c *ServerClient) clearToken()          { c.mu.Lock(); c.token = ""; c.mu.Unlock() }
