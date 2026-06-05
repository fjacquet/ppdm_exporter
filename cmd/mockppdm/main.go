// Command mockppdm is a minimal fake Dell PowerProtect Data Manager server for
// end-to-end demos. It serves the REST surface the exporter calls — bearer auth on
// POST /api/v2/login plus the per-resource list GETs — over self-signed TLS on :8443,
// returning canned JSON from embedded fixtures. It is NOT a faithful PPDM emulator;
// it exists so the Compose stack lights up Prometheus/Grafana without real hardware.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"io"
	"log"
	"math/big"
	"net/http"
	"time"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// routes maps an exporter request path to its embedded fixture file. PPDM list
// endpoints carry ?page=/?filter= query strings, which ServeMux ignores when matching.
var routes = map[string]string{
	"/api/v2/activities":          "fixtures/activities.json",
	"/api/v2/assets":              "fixtures/assets.json",
	"/api/v2/datadomain-mtrees":   "fixtures/datadomain-mtrees.json",
	"/api/v2/storage-systems":     "fixtures/storage-systems.json",
	"/api/v3/health-entities":     "fixtures/health-entities.json",
	"/api/v2/alerts":              "fixtures/alerts.json",
	"/api/v2/copies":              "fixtures/copies.json",
	"/api/v3/protection-policies": "fixtures/protection-policies.json",
}

const mockToken = "mockppdm-access-token"

func main() {
	mux := http.NewServeMux()

	// Bearer login: POST returns an access token in the JSON body. Credentials are
	// not checked — this is a demo server.
	mux.HandleFunc("/api/v2/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, []byte(`{"access_token":"`+mockToken+`","token_type":"Bearer","expires_in":1800,"refresh_token":"mock-refresh","scope":"IAMScope"}`))
	})

	for path, file := range routes {
		mux.HandleFunc(path, fixtureHandler(file))
	}

	srv := &http.Server{
		Addr:              ":8443",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{mustSelfSignedCert()},
		},
	}
	log.Println("mockppdm: serving fake PPDM API on https://localhost:8443")
	log.Fatal(srv.ListenAndServeTLS("", ""))
}

// fixtureHandler returns a GET handler that requires the bearer token and serves the
// named embedded fixture as JSON.
func fixtureHandler(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+mockToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, err := fixtures.ReadFile(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeBytes(w, b)
	}
}

// writeBytes writes b to w. It takes io.Writer (not http.ResponseWriter) so the raw
// write is isolated to one helper, the same pattern the tests use.
func writeBytes(w io.Writer, b []byte) { _, _ = w.Write(b) }

// mustSelfSignedCert generates an in-memory self-signed certificate at startup.
// Clients connect with insecureSkipVerify, so the cert only needs to be valid TLS.
func mustSelfSignedCert() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("mockppdm: generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mockppdm"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"mockppdm", "localhost"},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatalf("mockppdm: create cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
