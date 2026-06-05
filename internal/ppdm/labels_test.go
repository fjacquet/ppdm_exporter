package ppdm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

func keySet(s Sample) string {
	keys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		keys[i] = l.Key
	}
	return strings.Join(keys, ",")
}

// TestLabelKeySetConsistentPerMetric enforces the load-bearing invariant: a metric
// name must carry one ordered label-key set across all its series (ADR-0006).
func TestLabelKeySetConsistentPerMetric(t *testing.T) {
	m := ppdmclient.NewMock("ppdm01")
	for _, f := range []struct{ prefix, file string }{
		{"/api/v2/activities", "testdata/activities.json"},
		{"/api/v2/assets", "testdata/assets.json"},
		{"/api/v2/datadomain-mtrees", "testdata/datadomain-mtrees.json"},
		{"/api/v2/storage-systems", "testdata/storage-systems.json"},
		{"/api/v3/health-entities", "testdata/health-entities.json"},
		{"/api/v2/alerts", "testdata/alerts.json"},
	} {
		body, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		m.SetJSONPrefix(f.prefix, string(body))
	}

	collectors := []ResourceCollector{Activities{Lookback: time.Hour}, Assets{}, Capacity{}, Health{}}
	want := map[string]string{}
	for _, rc := range collectors {
		samples, err := rc.Collect(context.Background(), m)
		if err != nil {
			t.Fatalf("%s.Collect: %v", rc.Name(), err)
		}
		for _, s := range samples {
			s = s.WithServer("ppdm01")
			ks := keySet(s)
			if prev, ok := want[s.Name]; ok && prev != ks {
				t.Errorf("metric %s has inconsistent label keys: %q vs %q", s.Name, prev, ks)
			}
			want[s.Name] = ks
		}
	}
}
