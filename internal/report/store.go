package report

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations.sql
var schemaSQL string

// Store is the PostgreSQL backup-history store.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool to dsn.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// Migrate applies the idempotent schema (CREATE TABLE IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schemaSQL)
	return err
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

func ts(s string) *time.Time {
	if t, ok := parseTime(s); ok {
		return &t
	}
	return nil
}

// UpsertJobs inserts/updates backup_jobs by id (append-only events; re-capture is a no-op update).
func (s *Store) UpsertJobs(ctx context.Context, tenant, server string, jobs []Job, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, j := range jobs {
		b.Queue(`INSERT INTO backup_jobs
			(id,tenant,server,category,subcategory,result_status,asset_id,asset_name,policy_name,
			 started_at,completed_at,bytes_transferred,created_at,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (id, server) DO UPDATE SET result_status=EXCLUDED.result_status,
			 completed_at=EXCLUDED.completed_at, bytes_transferred=EXCLUDED.bytes_transferred,
			 captured_at=EXCLUDED.captured_at`,
			j.ID, tenant, server, j.Category, j.Subcategory, j.status(), j.Asset.ID, j.Asset.Name,
			j.ProtectionPolicy.Name, ts(j.StartedAt), ts(j.CompletedAt), int64(j.Result.BytesTransferred),
			ts(j.CreatedAt), capturedAt)
	}
	return s.sendBatch(ctx, b, len(jobs))
}

// UpsertCopies inserts/updates copies by id.
func (s *Store) UpsertCopies(ctx context.Context, tenant, server string, copies []Copy, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, c := range copies {
		b.Queue(`INSERT INTO copies
			(id,tenant,server,asset_id,policy_name,copy_type,create_time,expiration_time,retention_time,
			 retention_lock,storage_system_id,location,size_bytes,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (id, server) DO UPDATE SET expiration_time=EXCLUDED.expiration_time,
			 retention_time=EXCLUDED.retention_time, retention_lock=EXCLUDED.retention_lock,
			 captured_at=EXCLUDED.captured_at`,
			c.ID, tenant, server, c.AssetID, c.PolicyName, c.CopyType, ts(c.CreateTime),
			ts(c.ExpirationTime), ts(c.RetentionTime), c.RetentionLock, c.StorageSystemID,
			c.Location, int64(c.Size), capturedAt)
	}
	return s.sendBatch(ctx, b, len(copies))
}

// UpsertAssets upserts current asset protection state by id.
func (s *Store) UpsertAssets(ctx context.Context, tenant, server string, assets []Asset, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, a := range assets {
		b.Queue(`INSERT INTO assets
			(id,tenant,server,name,type,protection_status,last_available_copy_time,policy_name,updated_at,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id, server) DO UPDATE SET protection_status=EXCLUDED.protection_status,
			 last_available_copy_time=EXCLUDED.last_available_copy_time, policy_name=EXCLUDED.policy_name,
			 updated_at=EXCLUDED.updated_at, captured_at=EXCLUDED.captured_at`,
			a.ID, tenant, server, a.Name, a.Type, a.ProtectionStatus, ts(a.LastAvailableCopyTime),
			a.ProtectionPolicy.Name, capturedAt, capturedAt)
	}
	return s.sendBatch(ctx, b, len(assets))
}

// UpsertPolicies upserts protection policies by id, objectives as jsonb.
func (s *Store) UpsertPolicies(ctx context.Context, tenant, server string, policies []Policy, capturedAt time.Time) error {
	b := &pgx.Batch{}
	for _, p := range policies {
		obj, _ := json.Marshal(p.Objectives)
		b.Queue(`INSERT INTO protection_policies (id,tenant,server,name,objectives,updated_at,captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (id, server) DO UPDATE SET name=EXCLUDED.name, objectives=EXCLUDED.objectives,
			 updated_at=EXCLUDED.updated_at, captured_at=EXCLUDED.captured_at`,
			p.ID, tenant, server, p.Name, obj, capturedAt, capturedAt)
	}
	return s.sendBatch(ctx, b, len(policies))
}

func (s *Store) sendBatch(ctx context.Context, b *pgx.Batch, n int) error {
	if n == 0 {
		return nil
	}
	br := s.pool.SendBatch(ctx, b)
	defer func() { _ = br.Close() }()
	for i := 0; i < n; i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// JobWatermark returns the newest created_at for a server's jobs, or zero time if none.
func (s *Store) JobWatermark(ctx context.Context, server string) (time.Time, error) {
	return s.watermark(ctx, `SELECT max(created_at) FROM backup_jobs WHERE server=$1`, server)
}

// CopyWatermark returns the newest create_time for a server's copies, or zero time if none.
func (s *Store) CopyWatermark(ctx context.Context, server string) (time.Time, error) {
	return s.watermark(ctx, `SELECT max(create_time) FROM copies WHERE server=$1`, server)
}

func (s *Store) watermark(ctx context.Context, q, server string) (time.Time, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, q, server).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// Prune deletes append-only event rows (backup_jobs, copies) older than retentionDays.
// assets and protection_policies hold current upsert-latest state, not time-series events,
// so they are intentionally not pruned here (a decommissioned asset's last-known row is
// kept as part of the history until a future explicit reconciliation pass).
func (s *Store) Prune(ctx context.Context, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	if _, err := s.pool.Exec(ctx, `DELETE FROM backup_jobs WHERE created_at < $1`, cutoff); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM copies WHERE create_time < $1`, cutoff)
	return err
}

// SLATarget is a resolved per-asset SLA target row (the only materialized Phase 2 state).
type SLATarget struct {
	Tenant        string
	AssetType     string
	PolicyName    string
	RPOSeconds    int64
	RetentionDays int
	MinCopies     int
	GraceSeconds  int64
	Source        string // policy | override | default
}

// UpsertSLATargets idempotently writes resolved targets, keyed by (tenant, asset_type, policy_name).
func (s *Store) UpsertSLATargets(ctx context.Context, targets []SLATarget) error {
	b := &pgx.Batch{}
	for _, t := range targets {
		b.Queue(`INSERT INTO sla_targets
			(tenant,asset_type,policy_name,rpo_seconds,retention_days,min_copies,grace_seconds,source,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
			ON CONFLICT (tenant, asset_type, policy_name) DO UPDATE SET rpo_seconds=EXCLUDED.rpo_seconds,
			 retention_days=EXCLUDED.retention_days, min_copies=EXCLUDED.min_copies,
			 grace_seconds=EXCLUDED.grace_seconds, source=EXCLUDED.source, updated_at=EXCLUDED.updated_at`,
			t.Tenant, t.AssetType, t.PolicyName, t.RPOSeconds, t.RetentionDays, t.MinCopies, t.GraceSeconds, t.Source)
	}
	return s.sendBatch(ctx, b, len(targets))
}

// Rule321Row is one asset's 3-2-1-1-0 evaluation from the rule_321110 view.
type Rule321Row struct {
	AssetID, AssetName, AssetType                       string
	CopiesOk, MediaOk, OffsiteOk, ImmutableOk, ErrorsOk bool
	RulePass                                            bool
	CopiesCount, DistinctMedia, DistinctLocations       int
}

// Rule321Rows returns a tenant's per-asset 3-2-1-1-0 verdicts.
func (s *Store) Rule321Rows(ctx context.Context, tenant string) ([]Rule321Row, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT asset_id, asset_name, asset_type, copies_ok, media_ok, offsite_ok, immutable_ok,
		        errors_ok, rule_pass, copies_count, distinct_media, distinct_locations
		 FROM rule_321110 WHERE tenant=$1 ORDER BY asset_name`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule321Row
	for rows.Next() {
		var r Rule321Row
		if err := rows.Scan(&r.AssetID, &r.AssetName, &r.AssetType, &r.CopiesOk, &r.MediaOk,
			&r.OffsiteOk, &r.ImmutableOk, &r.ErrorsOk, &r.RulePass,
			&r.CopiesCount, &r.DistinctMedia, &r.DistinctLocations); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ComplianceRow is one asset's SLA verdict from the compliance view.
type ComplianceRow struct {
	AssetID, AssetName, AssetType, PolicyName string
	RPOOk, RetentionOk, CopiesOk, Compliant   bool
	Reasons                                   string
}

// ComplianceRows returns a tenant's per-asset SLA verdicts (non-compliant first).
func (s *Store) ComplianceRows(ctx context.Context, tenant string) ([]ComplianceRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT asset_id, asset_name, asset_type, policy_name, rpo_ok, retention_ok, copies_ok,
		        compliant, reasons
		 FROM compliance WHERE tenant=$1 ORDER BY compliant, asset_name`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComplianceRow
	for rows.Next() {
		var r ComplianceRow
		if err := rows.Scan(&r.AssetID, &r.AssetName, &r.AssetType, &r.PolicyName,
			&r.RPOOk, &r.RetentionOk, &r.CopiesOk, &r.Compliant, &r.Reasons); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Summary is the report's headline tally for a tenant.
type Summary struct {
	TotalAssets, CompliantAssets                   int
	RPOFailures, RetentionFailures, CopiesFailures int
	BadgePass                                      bool // every asset passes 3-2-1-1-0
}

// ReportSummary computes the headline counts for a tenant in one round-trip.
func (s *Store) ReportSummary(ctx context.Context, tenant string) (Summary, error) {
	var sm Summary
	err := s.pool.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM compliance WHERE tenant=$1),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND compliant),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND NOT rpo_ok),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND NOT retention_ok),
		 (SELECT count(*) FROM compliance WHERE tenant=$1 AND NOT copies_ok),
		 COALESCE((SELECT bool_and(rule_pass) FROM rule_321110 WHERE tenant=$1), false)`,
		tenant).Scan(&sm.TotalAssets, &sm.CompliantAssets, &sm.RPOFailures,
		&sm.RetentionFailures, &sm.CopiesFailures, &sm.BadgePass)
	return sm, err
}

// CapturedPolicy is a stored protection policy: its name plus the raw objectives JSON.
type CapturedPolicy struct {
	Name       string
	Objectives []byte
}

// CapturedPolicies returns a tenant's stored protection policies (across servers) for target
// resolution. Targets are tenant-scoped (assets join by tenant+policy_name, not server).
func (s *Store) CapturedPolicies(ctx context.Context, tenant string) ([]CapturedPolicy, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, COALESCE(objectives, 'null'::jsonb) FROM protection_policies WHERE tenant=$1`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CapturedPolicy
	for rows.Next() {
		var p CapturedPolicy
		if err := rows.Scan(&p.Name, &p.Objectives); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StartRun opens a capture_runs row and returns its id.
func (s *Store) StartRun(ctx context.Context, server, version string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO capture_runs (server, started_at, tool_version) VALUES ($1, now(), $2) RETURNING id`,
		server, version).Scan(&id)
	return id, err
}

// FinishRun closes a capture_runs row with outcome + per-resource counts.
func (s *Store) FinishRun(ctx context.Context, id int64, ok bool, errMsg string, counts map[string]int) error {
	cj, _ := json.Marshal(counts)
	_, err := s.pool.Exec(ctx,
		`UPDATE capture_runs SET finished_at=now(), ok=$2, error=$3, counts=$4 WHERE id=$1`,
		id, ok, errMsg, cj)
	return err
}

// DeliveryExists reports whether a SUCCESSFUL report delivery is already recorded for the
// (tenant, period) occurrence. Failed attempts return false so the scheduler retries them.
func (s *Store) DeliveryExists(ctx context.Context, tenant, period string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM report_deliveries WHERE tenant=$1 AND period=$2 AND ok)`,
		tenant, period).Scan(&exists)
	return exists, err
}

// RecordDelivery idempotently upserts the outcome of a delivery attempt for (tenant, period);
// a later success overwrites an earlier failure.
func (s *Store) RecordDelivery(ctx context.Context, tenant, period string, ok bool, errMsg string, recipients []string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO report_deliveries (tenant, period, sent_at, ok, error, recipients)
		 VALUES ($1,$2, now(), $3,$4,$5)
		 ON CONFLICT (tenant, period) DO UPDATE SET sent_at=now(), ok=EXCLUDED.ok,
		  error=EXCLUDED.error, recipients=EXCLUDED.recipients`,
		tenant, period, ok, errMsg, strings.Join(recipients, ","))
	return err
}
