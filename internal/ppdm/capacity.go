package ppdm

import (
	"context"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// mtree is one /api/v2/datadomain-mtrees content item (20.1.0 DataDomainMTree, ADR-0010).
// 20.1.0 exposes total + available; used is derived. No logical-used field exists.
type mtree struct {
	Name      string  `json:"name"`
	TotalCap  float64 `json:"totalCapacityInBytes"`
	Available float64 `json:"availableCapacityInBytes"`
}

// storageSystem is one /api/v2/storage-systems content item (20.1.0 StorageSystem, ADR-0010).
// DataDomain capacity is nested under details.dataDomain.
type storageSystem struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Details struct {
		DataDomain struct {
			TotalSize float64 `json:"totalSize"`
			TotalUsed float64 `json:"totalUsed"`
		} `json:"dataDomain"`
	} `json:"details"`
}

// Capacity collects storage-unit (MTree) and storage-system capacity in bytes.
type Capacity struct{}

func (Capacity) Name() string { return "capacity" }

func (Capacity) Collect(ctx context.Context, c ppdmclient.Client) ([]Sample, error) {
	mtrees, err := ppdmclient.GetAll[mtree](ctx, c, "/api/v2/datadomain-mtrees", 500)
	if err != nil {
		return nil, err
	}
	systems, err := ppdmclient.GetAll[storageSystem](ctx, c, "/api/v2/storage-systems", 500)
	if err != nil {
		return nil, err
	}
	var out []Sample
	for _, m := range mtrees {
		su := []Label{{Key: "storage_unit", Value: m.Name}}
		out = append(out,
			Sample{Name: "ppdm_storage_unit_physical_capacity_bytes", Value: m.TotalCap, Labels: su},
			Sample{Name: "ppdm_storage_unit_physical_used_bytes", Value: m.TotalCap - m.Available, Labels: su}, // derived: total minus available
		)
	}
	for _, s := range systems {
		sl := []Label{{Key: "storage_system", Value: s.Name}, {Key: "type", Value: s.Type}}
		out = append(out,
			Sample{Name: "ppdm_storage_system_total_bytes", Value: s.Details.DataDomain.TotalSize, Labels: sl},
			Sample{Name: "ppdm_storage_system_used_bytes", Value: s.Details.DataDomain.TotalUsed, Labels: sl},
		)
	}
	return out, nil
}
