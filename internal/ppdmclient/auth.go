package ppdmclient

import (
	"context"
	"fmt"
	"time"
)

// loginResp is the PPDM login response (POST /api/v2/login). Validated against 19.22.0.
type loginResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// ensureToken logs in if there is no cached token or it is within 60s of expiry.
func (c *ServerClient) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	valid := c.token != "" && time.Now().Before(c.expiresAt.Add(-60*time.Second))
	c.mu.Unlock()
	if valid {
		return nil
	}

	var lr loginResp
	resp, err := c.rc.R().SetContext(ctx).
		SetBody(map[string]string{"username": c.cfg.Username, "password": c.cfg.Password}).
		SetResult(&lr).
		Post("/api/v2/login")
	if err != nil {
		return fmt.Errorf("login POST: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("login POST: status %d", resp.StatusCode())
	}
	if lr.AccessToken == "" {
		return fmt.Errorf("login POST: empty access_token in response")
	}
	ttl := lr.ExpiresIn
	if ttl <= 0 {
		ttl = 1800
	}
	c.mu.Lock()
	c.token = lr.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(ttl) * time.Second)
	c.mu.Unlock()
	return nil
}

// Close is best-effort logout. PPDM access tokens expire on their own; there is no
// mandatory server-side logout, so this just drops the cached token.
func (c *ServerClient) Close() error { c.clearToken(); return nil }
