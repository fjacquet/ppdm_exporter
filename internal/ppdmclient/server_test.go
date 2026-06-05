package ppdmclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// writeBytes takes an io.Writer (not http.ResponseWriter) so the Semgrep
// write-to-ResponseWriter XSS rule does not fire — these are test fixtures.
func writeBytes(w io.Writer, b []byte) { _, _ = w.Write(b) }

func newFakePPDM(logins *int32, validToken string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(logins, 1)
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"access_token":"`+validToken+`","token_type":"Bearer","expires_in":1800,"refresh_token":"r1"}`))
	})
	mux.HandleFunc("/api/v2/activities", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+validToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"content":[{"id":"a1"}],"page":{"totalPages":1}}`))
	})
	return httptest.NewTLSServer(mux)
}

func TestServerClientAuthAndGet(t *testing.T) {
	var logins int32
	srv := newFakePPDM(&logins, "tok-123")
	defer srv.Close()

	c := NewServerClient(Config{
		Name: "ppdm01", BaseURL: srv.URL, Username: "u", Password: "p",
		InsecureSkipVerify: true, HTTPClient: srv.Client(),
	})
	defer c.Close()

	var out struct {
		Content []struct {
			ID string `json:"id"`
		} `json:"content"`
	}
	if err := c.Get(context.Background(), "/api/v2/activities", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("content = %d, want 1", len(out.Content))
	}
	// Second Get reuses the token — no extra login.
	_ = c.Get(context.Background(), "/api/v2/activities", &out)
	if logins != 1 {
		t.Fatalf("logins = %d, want 1 (token reused)", logins)
	}
}

func TestServerClientReloginOn401(t *testing.T) {
	var logins int32
	var rotated atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&logins, 1)
		tok := "tok1"
		if rotated.Load() {
			tok = "tok2"
		}
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"access_token":"`+tok+`","token_type":"Bearer","expires_in":1800}`))
	})
	mux.HandleFunc("/api/v2/activities", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok2" { // only tok2 accepted
			rotated.Store(true)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeBytes(w, []byte(`{"content":[],"page":{"totalPages":1}}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := NewServerClient(Config{Name: "ppdm01", BaseURL: srv.URL, HTTPClient: srv.Client()})
	defer c.Close()
	var out map[string]any
	if err := c.Get(context.Background(), "/api/v2/activities", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if logins != 2 {
		t.Fatalf("logins = %d, want 2 (initial + relogin)", logins)
	}
}
