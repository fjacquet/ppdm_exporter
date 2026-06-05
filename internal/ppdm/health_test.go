package ppdm

import (
	"context"
	"os"
	"testing"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func TestHealthCollect(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	for _, f := range []struct{ prefix, file string }{
		{"/api/v3/health-entities", "testdata/health-entities.json"},
		{"/api/v2/alerts", "testdata/alerts.json"},
	} {
		body, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		m.SetJSONPrefix(f.prefix, string(body))
	}

	got, err := Health{}.Collect(context.Background(), m)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	health := map[string]float64{}
	alerts := map[string]float64{}
	for _, s := range got {
		switch s.Name {
		case "ppdm_health_entity_status":
			health[s.LabelValue("entity")] = s.Value
		case "ppdm_alert_count":
			alerts[s.LabelValue("severity")+"|"+s.LabelValue("ack_state")] += s.Value
		}
	}
	if health["ppdm-server"] != 1 {
		t.Errorf("ppdm-server status = %v, want 1 (OK)", health["ppdm-server"])
	}
	if health["dd-storage"] != 0 {
		t.Errorf("dd-storage status = %v, want 0 (WARNING)", health["dd-storage"])
	}
	if alerts["CRITICAL|NOT_ACKNOWLEDGED"] != 2 {
		t.Errorf("CRITICAL/NOT_ACKNOWLEDGED = %v, want 2", alerts["CRITICAL|NOT_ACKNOWLEDGED"])
	}
	if alerts["WARNING|ACKNOWLEDGED"] != 1 {
		t.Errorf("WARNING/ACKNOWLEDGED = %v, want 1", alerts["WARNING|ACKNOWLEDGED"])
	}
}
