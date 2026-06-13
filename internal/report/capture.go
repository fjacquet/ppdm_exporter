package report

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Capturer pulls authoritative PPDM records for one server and persists them.
type Capturer struct {
	store      *Store
	version    string
	retention  config.Retention
	compliance config.Compliance
}

// NewCapturer wires a capturer to a store. retention drives per-tenant prune + backfill; compliance
// drives post-capture SLA target resolution.
func NewCapturer(store *Store, version string, retention config.Retention, compliance config.Compliance) *Capturer {
	return &Capturer{store: store, version: version, retention: retention, compliance: compliance}
}

// ServerClient pairs a tenant with its PPDM client (built by main from config).
type ServerClient struct {
	Tenant string
	Client ppdmclient.Client
}

// NewServerClient pairs a tenant with a PPDM client for RunOnce/Run.
func NewServerClient(tenant string, client ppdmclient.Client) ServerClient {
	return ServerClient{Tenant: tenant, Client: client}
}

// CaptureServer captures jobs/copies (incremental) + assets/policies (full) for one server,
// upserts them tagged with tenant, and records a capture_runs provenance row.
func (c *Capturer) CaptureServer(ctx context.Context, tenant string, client ppdmclient.Client) error {
	server := client.Name()
	runID, err := c.store.StartRun(ctx, server, c.version)
	if err != nil {
		return err
	}
	now := time.Now()
	counts, capErr := c.capture(ctx, tenant, server, client, now)
	msg := ""
	if capErr != nil {
		msg = capErr.Error()
		log.WithFields(log.Fields{"server": server, "err": capErr}).Warn("capture failed")
	} else if err := c.ResolveTargets(ctx, tenant, c.compliance); err != nil {
		// SLA target resolution is a post-capture step: a failure is logged and the captured
		// history still stands. It must not block this server's run or the others.
		log.WithFields(log.Fields{"server": server, "tenant": tenant, "err": err}).
			Warn("resolve SLA targets failed")
	}
	// Record the outcome on a detached context: if the capture timed out, ctx is already
	// done and FinishRun on it would silently abandon the provenance row.
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.store.FinishRun(finishCtx, runID, capErr == nil, msg, counts)
	return capErr
}

func (c *Capturer) capture(ctx context.Context, tenant, server string, client ppdmclient.Client, now time.Time) (map[string]int, error) {
	counts := map[string]int{}

	// Jobs (incremental by createTime watermark; bootstrap to retention window on first run).
	jobWM, err := c.store.JobWatermark(ctx, server)
	if err != nil {
		return counts, err
	}
	jobs, err := ppdmclient.GetAll[Job](ctx, client, activitiesPath(c.bootstrap(tenant, jobWM)), 500)
	if err != nil {
		return counts, fmt.Errorf("activities: %w", err)
	}
	if err := c.store.UpsertJobs(ctx, tenant, server, jobs, now); err != nil {
		return counts, err
	}
	counts["jobs"] = len(jobs)

	// Copies (incremental by createTime watermark).
	copyWM, err := c.store.CopyWatermark(ctx, server)
	if err != nil {
		return counts, err
	}
	copies, err := ppdmclient.GetAll[Copy](ctx, client, copiesPath(c.bootstrap(tenant, copyWM)), 500)
	if err != nil {
		return counts, fmt.Errorf("copies: %w", err)
	}
	if err := c.store.UpsertCopies(ctx, tenant, server, copies, now); err != nil {
		return counts, err
	}
	counts["copies"] = len(copies)

	// Assets + policies (full state each cycle).
	assets, err := ppdmclient.GetAll[Asset](ctx, client, "/api/v2/assets", 500)
	if err != nil {
		return counts, fmt.Errorf("assets: %w", err)
	}
	if err := c.store.UpsertAssets(ctx, tenant, server, assets, now); err != nil {
		return counts, err
	}
	counts["assets"] = len(assets)

	policies, err := ppdmclient.GetAll[Policy](ctx, client, "/api/v3/protection-policies", 500)
	if err != nil {
		return counts, fmt.Errorf("policies: %w", err)
	}
	if err := c.store.UpsertPolicies(ctx, tenant, server, policies, now); err != nil {
		return counts, err
	}
	counts["policies"] = len(policies)
	return counts, nil
}

// bootstrap returns the watermark, or now minus the tenant's retention window when there is no prior
// data, so the first capture backfills that tenant's history without fetching the entire server.
func (c *Capturer) bootstrap(tenant string, wm time.Time) time.Time {
	if wm.IsZero() {
		return time.Now().AddDate(0, 0, -c.retention.DaysFor(tenant))
	}
	return wm
}

// The watermark filter uses `ge` (>=), not `gt`: it re-fetches the boundary record each
// cycle (cheap — the upsert is a no-op) but cannot skip records sharing the watermark
// timestamp, which `gt` would.
func activitiesPath(since time.Time) string {
	return "/api/v2/activities?filter=" + url.QueryEscape(`createTime ge "`+since.UTC().Format(time.RFC3339)+`"`)
}

func copiesPath(since time.Time) string {
	return "/api/v2/copies?filter=" + url.QueryEscape(`createTime ge "`+since.UTC().Format(time.RFC3339)+`"`)
}

// RunOnce captures every server once (in parallel) and prunes beyond retention.
func (c *Capturer) RunOnce(ctx context.Context, servers []ServerClient, timeout time.Duration) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, sc := range servers {
		sc := sc
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, timeout)
			defer cancel()
			_ = c.CaptureServer(cctx, sc.Tenant, sc.Client) // errors recorded per server
			return nil
		})
	}
	_ = g.Wait()
	overrides := make(map[string]int, len(c.retention.Overrides))
	for _, o := range c.retention.Overrides {
		overrides[o.Tenant] = o.Days
	}
	if err := c.store.Prune(ctx, c.retention.DefaultDays, overrides); err != nil {
		log.WithError(err).Warn("prune failed")
	}
}

// Run loops RunOnce on interval until ctx is cancelled.
func (c *Capturer) Run(ctx context.Context, servers []ServerClient, interval, timeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.RunOnce(ctx, servers, timeout)
		}
	}
}
