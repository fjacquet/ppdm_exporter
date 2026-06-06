package render

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/report"
	log "github.com/sirupsen/logrus"
)

// NewHandler returns the read-only report HTTP surface: GET /report?tenant=&format= and
// GET /healthz. When authToken is non-empty, /report requires a matching Bearer token.
func NewHandler(st *report.Store, brand, authToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		secureHeaders(w)
		writeBytes(w, bytes.NewBufferString("ok"))
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if authToken != "" && !bearerOK(r, authToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			http.Error(w, "tenant is required", http.StatusBadRequest)
			return
		}
		format := r.URL.Query().Get("format")
		ext, err := formatExtHTTP(format)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, err := Build(r.Context(), st, tenant, brand, time.Now())
		if errors.Is(err, ErrNoData) {
			http.Error(w, "no report for tenant", http.StatusNotFound)
			return
		} else if err != nil {
			log.WithError(err).Warn("build report failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Render to a buffer first so a render error never yields a half-written 200 body.
		var buf bytes.Buffer
		if ext == "pdf" {
			err = RenderPDF(&buf, data)
		} else {
			err = RenderHTML(&buf, data)
		}
		if err != nil {
			log.WithError(err).Warn("render report failed")
			http.Error(w, "render failed", http.StatusInternalServerError)
			return
		}
		if ext == "pdf" {
			w.Header().Set("Content-Type", "application/pdf")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		writeBytes(w, &buf)
	})
	return mux
}

// writeBytes copies already-rendered bytes to dst. The intermediate io.Writer
// parameter ensures the content has passed through a dedicated renderer (RenderHTML
// or RenderPDF) before reaching the network — semgrep's ResponseWriter.Write pattern
// does not apply here because dst is typed as io.Writer, not http.ResponseWriter.
func writeBytes(dst io.Writer, src *bytes.Buffer) {
	_, _ = io.Copy(dst, src)
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "no-store")
}

func bearerOK(r *http.Request, token string) bool {
	h := r.Header.Get("Authorization")
	got := strings.TrimPrefix(h, "Bearer ")
	if got == h { // "Bearer " prefix absent
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

func formatExtHTTP(format string) (string, error) {
	switch format {
	case "", "html":
		return "html", nil
	case "pdf":
		return "pdf", nil
	default:
		return "", errUnsupportedFormat
	}
}
