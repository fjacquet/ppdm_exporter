package ppdm

import (
	"context"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Collector runs the background loop: every interval it polls all servers in parallel
// and publishes a fresh Snapshot. One server's failure never blocks others.
type Collector struct {
	clients    []ppdmclient.Client
	collectors []ResourceCollector
	store      *SnapshotStore
	interval   time.Duration
	timeout    time.Duration
}

// NewCollector wires the loop.
func NewCollector(clients []ppdmclient.Client, collectors []ResourceCollector, store *SnapshotStore, interval, timeout time.Duration) *Collector {
	return &Collector{clients: clients, collectors: collectors, store: store, interval: interval, timeout: timeout}
}

// CollectOnce runs a single cycle, stores, and returns the snapshot.
func (c *Collector) CollectOnce(ctx context.Context) *Snapshot {
	snap := c.collectAll(ctx)
	c.store.Store(snap)
	return snap
}

// Run loops until ctx is cancelled (assumes CollectOnce already primed the store).
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.store.Store(c.collectAll(ctx))
		}
	}
}

func (c *Collector) collectAll(ctx context.Context) *Snapshot {
	results := make([]*ServerSnapshot, len(c.clients))
	g, gctx := errgroup.WithContext(ctx)
	for i, client := range c.clients {
		i, client := i, client
		g.Go(func() error {
			results[i] = c.collectServer(gctx, client)
			return nil // graceful degradation
		})
	}
	_ = g.Wait()
	return &Snapshot{BuiltAt: time.Now(), Servers: results}
}

func (c *Collector) collectServer(ctx context.Context, client ppdmclient.Client) *ServerSnapshot {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ss := &ServerSnapshot{Server: client.Name(), LastScrape: time.Now(), OK: true}
	serverUp := 1.0
	for _, rc := range c.collectors {
		samples, err := rc.Collect(ctx, client)
		up := 1.0
		if err != nil {
			up = 0
			serverUp = 0 // any collector failing marks the server degraded
			log.WithFields(log.Fields{"server": client.Name(), "collector": rc.Name(), "err": err}).
				Warn("collector failed")
		}
		ss.Samples = append(ss.Samples, Sample{
			Name: "ppdm_collector_up", Labels: []Label{{Key: "collector", Value: rc.Name()}}, Value: up,
		}.WithServer(client.Name()))
		for _, s := range samples {
			ss.Samples = append(ss.Samples, s.WithServer(client.Name()))
		}
	}
	if serverUp == 0 {
		ss.OK = false
		ss.Err = "one or more collectors failed"
	}
	ss.Samples = append(ss.Samples, Sample{Name: "ppdm_up", Value: serverUp}.WithServer(client.Name()))
	return ss
}
