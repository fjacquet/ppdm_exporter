package ppdm

import (
	"testing"
	"time"
)

func TestSnapshotStoreLoadEmpty(t *testing.T) {
	st := NewSnapshotStore()
	if st.Load() == nil {
		t.Fatal("Load() on fresh store must return non-nil empty snapshot")
	}
	if n := len(st.Load().Servers); n != 0 {
		t.Fatalf("fresh snapshot has %d servers, want 0", n)
	}
}

func TestSnapshotMetricNamesAndSamplesByName(t *testing.T) {
	snap := &Snapshot{BuiltAt: time.Now(), Servers: []*ServerSnapshot{{
		Server: "ppdm01", OK: true,
		Samples: []Sample{
			{Name: "ppdm_asset_count", Labels: []Label{{"server", "ppdm01"}}, Value: 3},
			{Name: "ppdm_asset_count", Labels: []Label{{"server", "ppdm01"}}, Value: 5},
			{Name: "ppdm_up", Labels: []Label{{"server", "ppdm01"}}, Value: 1},
		},
	}}}
	st := NewSnapshotStore()
	st.Store(snap)
	names := st.Load().MetricNames()
	if len(names) != 2 { // deduped, sorted
		t.Fatalf("MetricNames = %v, want 2 distinct", names)
	}
	if got := st.Load().SamplesByName("ppdm_asset_count"); len(got) != 2 {
		t.Fatalf("SamplesByName(ppdm_asset_count) = %d, want 2", len(got))
	}
}
