// Package report captures PPDM backup history into a durable store for assurance reporting.
package report

import "time"

// parseTime parses an RFC3339 timestamp (with or without sub-second precision),
// returning ok=false for empty/unparseable input. Sub-second support matters: PPDM
// emits fractional seconds, and a dropped timestamp would store NULL — which the
// watermark and retention prune (NULL < cutoff is NULL) silently ignore.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Job is one /api/v2/activities record (a backup/restore job — immutable event).
type Job struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	State       string `json:"state"`
	CreateTime  string `json:"createTime"`
	EndTime     string `json:"endTime"`
	Result      struct {
		Status           string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
	Asset struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"asset"`
	ProtectionPolicy struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"`
}

func (j Job) status() string {
	if j.Result.Status != "" {
		return j.Result.Status
	}
	return j.State
}

// Copy is one /api/v2/latest-copies record (20.1.0 Copy, ADR-0010). policyName and
// expirationTime have no 20.1.0 source and are not captured (columns remain, NULL).
type Copy struct {
	ID              string  `json:"id"`
	AssetID         string  `json:"assetId"`
	CopyType        string  `json:"copyType"`
	CreateTime      string  `json:"createTime"`
	RetentionTime   string  `json:"retentionTime"`
	RetentionLock   string  `json:"retentionLock"` // enum ALL_COPIES_UNLOCKED|ALL_COPIES_LOCKED|PARTIAL_COPIES_LOCKED
	StorageSystemID string  `json:"storageSystemId"`
	Location        string  `json:"location"`
	Size            float64 `json:"size"`
}

// locked reports whether this copy is retention-locked (wholly or partially), feeding the has_immutable rollup.
func (c Copy) locked() bool {
	return c.RetentionLock == "ALL_COPIES_LOCKED" || c.RetentionLock == "PARTIAL_COPIES_LOCKED"
}

// Asset is one /api/v2/assets record (current protection state; 20.1.0 Asset, ADR-0010).
type Asset struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ProtectionStatus      string `json:"protectionStatus"`
	LastAvailableCopyTime string `json:"lastAvailableCopyTime"`
	ProtectionPolicy      struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"`
}

// Policy is one /api/v3/protection-policies record. Objectives kept as raw JSON.
type Policy struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Objectives any    `json:"objectives"` // stored as jsonb
}
