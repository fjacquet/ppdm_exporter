package ppdm

import "github.com/prometheus/client_golang/prometheus"

// PromCollector is an unchecked Prometheus collector: Describe emits nothing so the
// metric-name set can vary per snapshot. Collect reads the latest snapshot.
type PromCollector struct {
	store *SnapshotStore
}

// NewPromCollector wraps the snapshot store as a prometheus.Collector.
func NewPromCollector(store *SnapshotStore) *PromCollector { return &PromCollector{store: store} }

// Describe sends nothing (unchecked collector).
func (p *PromCollector) Describe(chan<- *prometheus.Desc) {}

// Collect turns every snapshot sample into a gauge metric.
func (p *PromCollector) Collect(ch chan<- prometheus.Metric) {
	snap := p.store.Load()
	for _, sv := range snap.Servers {
		for _, s := range sv.Samples {
			keys := make([]string, len(s.Labels))
			vals := make([]string, len(s.Labels))
			for i, l := range s.Labels {
				keys[i], vals[i] = l.Key, l.Value
			}
			desc := prometheus.NewDesc(s.Name, "PPDM metric "+s.Name, keys, nil)
			m, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, s.Value, vals...)
			if err != nil {
				continue // skip inconsistent label sets rather than panic
			}
			ch <- m
		}
	}
}
