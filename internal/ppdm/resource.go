package ppdm

import (
	"context"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// ResourceCollector collects one metric domain from a single PPDM server. It returns
// server-agnostic samples; the loop stamps the `server` label. Implementations own
// their endpoint path and JSON struct so provisional-API risk is localized.
type ResourceCollector interface {
	Name() string
	Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error)
}

// Registry is the ordered set of collectors run for every server.
func Registry(lookback, assetAgeThreshold time.Duration, perJobActivities bool) []ResourceCollector {
	return []ResourceCollector{
		Activities{Lookback: lookback, PerJob: perJobActivities},
		Assets{AgeThreshold: assetAgeThreshold},
		Capacity{},
		Health{},
	}
}
