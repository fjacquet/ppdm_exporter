package render

import (
	"net/http"
	"testing"
)

func req(bearer string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/report", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func TestAuthorizerScopedAndAdmin(t *testing.T) {
	a := NewAuthorizer("admintok", []TokenScope{
		{Token: "acmetok", Tenants: []string{"acme"}},
		{Token: "alltok", Tenants: []string{"*"}},
	})
	if !a.Required() {
		t.Fatal("Required should be true")
	}
	// scoped token: allowed for its tenant, denied for another
	sc, ok := a.Authenticate(req("acmetok"))
	if !ok || !a.Allows(sc, "acme") || a.Allows(sc, "globex") {
		t.Errorf("acme scope: ok=%v acme=%v globex=%v", ok, a.Allows(sc, "acme"), a.Allows(sc, "globex"))
	}
	// "*" token: any tenant
	sc, ok = a.Authenticate(req("alltok"))
	if !ok || !a.Allows(sc, "anything") {
		t.Errorf("* token should allow any tenant")
	}
	// admin authToken: any tenant
	sc, ok = a.Authenticate(req("admintok"))
	if !ok || !a.Allows(sc, "whatever") {
		t.Errorf("admin token should allow any tenant")
	}
	// unknown / missing token: not authenticated
	if _, ok := a.Authenticate(req("nope")); ok {
		t.Error("unknown token should not authenticate")
	}
	if _, ok := a.Authenticate(req("")); ok {
		t.Error("missing bearer should not authenticate")
	}
}

func TestAuthorizerNotRequiredWhenEmpty(t *testing.T) {
	a := NewAuthorizer("", nil)
	if a.Required() {
		t.Error("Required should be false with no tokens")
	}
}
