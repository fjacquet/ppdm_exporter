package ppdmclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetAllWalksPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"access_token":"t","token_type":"Bearer","expires_in":1800}`))
	})
	mux.HandleFunc("/api/v2/assets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "", "0":
			writeBytes(w, []byte(`{"content":[{"id":"a"},{"id":"b"}],"page":{"number":0,"totalPages":2}}`))
		default:
			writeBytes(w, []byte(`{"content":[{"id":"c"}],"page":{"number":1,"totalPages":2}}`))
		}
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	c := NewServerClient(Config{Name: "ppdm01", BaseURL: srv.URL, HTTPClient: srv.Client()})
	defer func() { _ = c.Close() }()

	type asset struct {
		ID string `json:"id"`
	}
	got, err := GetAll[asset](context.Background(), c, "/api/v2/assets", 500)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetAll returned %d items, want 3 across 2 pages", len(got))
	}
}

func TestGetAllAppendsToExistingQuery(t *testing.T) {
	var gotRawQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"access_token":"t","token_type":"Bearer","expires_in":1800}`))
	})
	mux.HandleFunc("/api/v2/activities", func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"content":[],"page":{"totalPages":1}}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	c := NewServerClient(Config{Name: "ppdm01", BaseURL: srv.URL, HTTPClient: srv.Client()})
	defer func() { _ = c.Close() }()

	type act struct{}
	path := "/api/v2/activities?filter=" + url.QueryEscape(`createdAt ge "x"`)
	if _, err := GetAll[act](context.Background(), c, path, 100); err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	// Both the original filter and the pagination params must be present (single '?').
	if !contains(gotRawQuery, "filter=") || !contains(gotRawQuery, "page=0") || !contains(gotRawQuery, "pageSize=100") {
		t.Fatalf("query = %q, want filter + page + pageSize", gotRawQuery)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
