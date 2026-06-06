package render

import (
	"crypto/sha256"
	"net/http"
	"strings"
)

// TokenScope is one bearer token and the tenants it may read ("*" = all). Plain DTO so the render
// package stays free of internal/config; cmd/report maps config tokens into these.
type TokenScope struct {
	Token   string
	Tenants []string
}

// Scope is an authenticated token's authorization: all tenants, or a specific set.
type Scope struct {
	all     bool
	tenants map[string]bool
}

// Authorizer maps bearer tokens (by sha256 hash) to their Scope.
type Authorizer struct {
	byHash map[[32]byte]Scope
}

// NewAuthorizer builds the registry. A non-empty authToken is registered as an all-tenants (admin)
// token; empty token entries are ignored. Required() is false only when nothing was registered.
func NewAuthorizer(authToken string, scopes []TokenScope) *Authorizer {
	a := &Authorizer{byHash: make(map[[32]byte]Scope)}
	if authToken != "" {
		a.byHash[sha256.Sum256([]byte(authToken))] = Scope{all: true}
	}
	for _, ts := range scopes {
		if ts.Token == "" {
			continue
		}
		sc := Scope{tenants: make(map[string]bool, len(ts.Tenants))}
		for _, t := range ts.Tenants {
			if t == "*" {
				sc.all = true
			} else {
				sc.tenants[t] = true
			}
		}
		a.byHash[sha256.Sum256([]byte(ts.Token))] = sc
	}
	return a
}

// Required reports whether any token is registered (i.e. auth must be enforced).
func (a *Authorizer) Required() bool { return len(a.byHash) > 0 }

// Authenticate matches a request's Bearer token to its Scope; ok=false if the token is absent or
// unknown. Lookup is a single map access on the sha256 hash (no per-token comparison).
func (a *Authorizer) Authenticate(r *http.Request) (Scope, bool) {
	h := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(h, "Bearer ")
	if tok == h { // "Bearer " prefix absent
		return Scope{}, false
	}
	sc, ok := a.byHash[sha256.Sum256([]byte(tok))]
	return sc, ok
}

// Allows reports whether an authenticated Scope may read tenant.
func (a *Authorizer) Allows(s Scope, tenant string) bool {
	return s.all || s.tenants[tenant]
}
