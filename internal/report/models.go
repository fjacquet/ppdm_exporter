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
	CreatedAt   string `json:"createdAt"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	Result      struct {
		Status           string  `json:"status"`
		BytesTransferred float64 `json:"bytesTransferred"`
	} `json:"result"`
	Asset struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"asset"` // provisional
	ProtectionPolicy struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"` // provisional
}

func (j Job) status() string {
	if j.Result.Status != "" {
		return j.Result.Status
	}
	return j.State
}

// Copy is one /api/v2/copies record (a backup copy with retention + location). Provisional.
type Copy struct {
	ID              string  `json:"id"`
	AssetID         string  `json:"assetId"`         // provisional
	PolicyName      string  `json:"policyName"`      // provisional
	CopyType        string  `json:"copyType"`        // provisional
	CreateTime      string  `json:"createTime"`      // provisional
	ExpirationTime  string  `json:"expirationTime"`  // provisional
	RetentionTime   string  `json:"retentionTime"`   // provisional
	RetentionLock   bool    `json:"retentionLock"`   // provisional
	StorageSystemID string  `json:"storageSystemId"` // provisional
	Location        string  `json:"location"`        // provisional
	Size            float64 `json:"size"`            // provisional
}

// Asset is one /api/v2/assets record (current protection state).
type Asset struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	ProtectionStatus      string `json:"protectionStatus"`      // provisional
	LastAvailableCopyTime string `json:"lastAvailableCopyTime"` // provisional
	ProtectionPolicy      struct {
		Name string `json:"name"`
	} `json:"protectionPolicy"` // provisional
}

// Policy is one /api/v3/protection-policies record. Objectives kept as raw JSON.
type Policy struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Objectives any    `json:"objectives"` // provisional; stored as jsonb
}
