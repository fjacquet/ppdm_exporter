// Package ppdm holds the PPDM metric model, snapshot store, modular collectors,
// and the Prometheus + OTLP export paths.
package ppdm

// Label is a single Prometheus label key/value.
type Label struct {
	Key   string
	Value string
}

// Sample is one metric data point: a name, an ordered label set, and a value.
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
}

// LabelValue returns the value of the named label, or "" if absent.
func (s Sample) LabelValue(key string) string {
	for _, l := range s.Labels {
		if l.Key == key {
			return l.Value
		}
	}
	return ""
}

// WithServer returns a copy with a leading {server=name} label. Collectors emit
// server-agnostic samples; the collection loop stamps the server identity.
func (s Sample) WithServer(name string) Sample {
	labels := make([]Label, 0, len(s.Labels)+1)
	labels = append(labels, Label{Key: "server", Value: name})
	labels = append(labels, s.Labels...)
	return Sample{Name: s.Name, Labels: labels, Value: s.Value}
}
