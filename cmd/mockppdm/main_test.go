package main

import "testing"

func TestRoutesServeProtectionRules(t *testing.T) {
	for _, p := range []string{"/api/v3/rules", "/api/v3/protection-policy-rule-metrics"} {
		if _, ok := routes[p]; !ok {
			t.Errorf("mockppdm has no route for %s", p)
		}
	}
}
