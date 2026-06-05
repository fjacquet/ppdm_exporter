package ppdm

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPromCollectorEmitsSnapshot(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{
		BuiltAt: time.Now(),
		Servers: []*ServerSnapshot{{
			Server: "ppdm01", OK: true,
			Samples: []Sample{
				{Name: "ppdm_activity_count", Value: 1, Labels: []Label{
					{Key: "server", Value: "ppdm01"},
					{Key: "category", Value: "PROTECT"},
					{Key: "result_status", Value: "SUCCESS"},
				}},
			},
		}},
	})

	want := `
# HELP ppdm_activity_count PPDM metric ppdm_activity_count
# TYPE ppdm_activity_count gauge
ppdm_activity_count{category="PROTECT",result_status="SUCCESS",server="ppdm01"} 1
`
	if err := testutil.CollectAndCompare(NewPromCollector(store), strings.NewReader(want), "ppdm_activity_count"); err != nil {
		t.Fatal(err)
	}
}
