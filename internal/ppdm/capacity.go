package ppdm

import (
	"context"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
)

// mtree is the provisional shape of a /api/v2/datadomain-mtrees content item.
// Capacity field names are unconfirmed by every source (ambiguous in the PDF) — ADR-0009.
type mtree struct {
	Name         string  `json:"name"`                  // provisional
	PhysicalCap  float64 `json:"physicalCapacityBytes"` // provisional
	PhysicalUsed float64 `json:"physicalUsedBytes"`     // provisional
	LogicalUsed  float64 `json:"logicalUsedBytes"`      // provisional
}

// storageSystem is the provisional shape of a /api/v2/storage-systems content item.
type storageSystem struct {
	Name      string  `json:"name"`           // provisional
	Type      string  `json:"type"`           // provisional
	TotalSize float64 `json:"totalSizeBytes"` // provisional
	UsedSize  float64 `json:"usedSizeBytes"`  // provisional
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
			Sample{Name: "ppdm_storage_unit_physical_capacity_bytes", Value: m.PhysicalCap, Labels: su},
			Sample{Name: "ppdm_storage_unit_physical_used_bytes", Value: m.PhysicalUsed, Labels: su},
			Sample{Name: "ppdm_storage_unit_logical_used_bytes", Value: m.LogicalUsed, Labels: su},
		)
	}
	for _, s := range systems {
		sl := []Label{{Key: "storage_system", Value: s.Name}, {Key: "type", Value: s.Type}}
		out = append(out,
			Sample{Name: "ppdm_storage_system_total_bytes", Value: s.TotalSize, Labels: sl},
			Sample{Name: "ppdm_storage_system_used_bytes", Value: s.UsedSize, Labels: sl},
		)
	}
	return out, nil
}
