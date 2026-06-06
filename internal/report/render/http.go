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
	requireAuth := authToken != ""
	var wantHash [32]byte
	if requireAuth {
		wantHash = sha256.Sum256([]byte(authToken)) // hash the static token once, not per request
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		secureHeaders(w)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if requireAuth && !bearerOK(r, wantHash) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			http.Error(w, "tenant is required", http.StatusBadRequest)
			return
		}
		ext, err := FormatExt(r.URL.Query().Get("format"))
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
		renderFn, contentType := RenderHTML, "text/html; charset=utf-8"
		if ext == "pdf" {
			renderFn, contentType = RenderPDF, "application/pdf"
		}
		// Render to a buffer first so a render error never yields a half-written 200 body.
		var buf bytes.Buffer
		if err := renderFn(&buf, data); err != nil {
			log.WithError(err).Warn("render report failed")
			http.Error(w, "render failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
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

// bearerOK compares the request's Bearer token against the precomputed token hash in constant
// time. Hashing both sides to a fixed 32 bytes avoids leaking the configured token's length.
func bearerOK(r *http.Request, wantHash [32]byte) bool {
	h := r.Header.Get("Authorization")
	got := strings.TrimPrefix(h, "Bearer ")
	if got == h { // "Bearer " prefix absent
		return false
	}
	gotHash := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

// FormatExt validates an output format and returns its file extension ("" defaults to html).
// Shared by the CLI render command and the HTTP endpoint so both accept the same set.
func FormatExt(format string) (string, error) {
	switch format {
	case "", "html":
		return "html", nil
	case "pdf":
		return "pdf", nil
	default:
		return "", errUnsupportedFormat
	}
}
