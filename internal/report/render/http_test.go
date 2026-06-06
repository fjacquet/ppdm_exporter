package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fjacquet/ppdm_exporter/internal/report/reporttest"
)

func TestHandlerServesReport(t *testing.T) {
	st := storeFor(t)                  // from data_test.go: tenant acme has assets
	h := NewHandler(st, "Acme Co", "") // no auth
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/report?tenant=acme&format=html", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
}

func TestHandlerValidation(t *testing.T) {
	st := storeFor(t)
	h := NewHandler(st, "Acme Co", "tok")
	srv := httptest.NewServer(h)
	defer srv.Close()

	doResp := func(path string, bearer string) *http.Response {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	do := func(path string, bearer string) int {
		resp := doResp(path, bearer)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if c := do("/report?tenant=acme", ""); c != 401 {
		t.Errorf("no token -> %d, want 401", c)
	}
	if c := do("/report?tenant=acme", "wrong"); c != 401 {
		t.Errorf("bad token -> %d, want 401", c)
	}
	// RFC 7235: 401 responses must include WWW-Authenticate.
	if resp401 := doResp("/report?tenant=acme", "wrong"); true {
		_ = resp401.Body.Close()
		if got := resp401.Header.Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate = %q, want \"Bearer\"", got)
		}
	}
	if c := do("/report", "tok"); c != 400 {
		t.Errorf("missing tenant -> %d, want 400", c)
	}
	if c := do("/report?tenant=ghost", "tok"); c != 404 {
		t.Errorf("unknown tenant -> %d, want 404", c)
	}
	if c := do("/report?tenant=acme", "tok"); c != 200 {
		t.Errorf("good token -> %d, want 200", c)
	}
}

func TestHandlerRejectsNonGET(t *testing.T) {
	st := reporttest.NewStore(t)
	srv := httptest.NewServer(NewHandler(st, "B", ""))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/report?tenant=acme", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST -> %d, want 405", resp.StatusCode)
	}
}
