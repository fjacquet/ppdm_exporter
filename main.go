// Command ppdm_exporter is a Prometheus + OTLP exporter for Dell PowerProtect Data Manager.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/ppdm"
	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var cfgPath string
	var once, debug bool
	root := &cobra.Command{
		Use:     "ppdm_exporter",
		Short:   "Prometheus + OTLP exporter for Dell PowerProtect Data Manager",
		Version: version,
		RunE: func(_ *cobra.Command, _ []string) error {
			return run(cfgPath, once, debug)
		},
	}
	root.Flags().StringVar(&cfgPath, "config", "config.yaml", "path to config file")
	root.Flags().BoolVar(&once, "once", false, "run a single collection cycle and exit")
	root.Flags().BoolVar(&debug, "debug", false, "verbose logging")
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(cfgPath string, once, debug bool) error {
	if debug {
		log.SetLevel(log.DebugLevel)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := ppdm.NewSnapshotStore()

	// Optional OTLP metric export (dual export alongside /metrics).
	var otlpExp *ppdm.OTLPExporter
	if cfg.OTel.Enabled {
		e, oerr := ppdm.NewOTLPExporter(ctx, cfg.OTel.Endpoint, cfg.OTel.Insecure, cfg.OTel.Interval, store, version)
		if oerr != nil {
			log.WithError(oerr).Warn("OTLP export disabled")
		} else {
			otlpExp = e
			defer func() {
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = otlpExp.Shutdown(sctx)
			}()
		}
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(ppdm.NewPromCollector(store))
	mux := http.NewServeMux()
	mux.Handle(cfg.Server.URI, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { healthHandler(w, store) })
	srv := &http.Server{Addr: cfg.Server.Host + ":" + cfg.Server.Port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// Serve before the first collection cycle (a slow first login must not block /metrics).
	if !once {
		go func() {
			log.WithField("addr", srv.Addr).Info("serving metrics")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error(err)
			}
		}()
	}

	// activeLoop owns the clients + cancel func of the currently-running collection
	// loop, so a config reload can stop it and swap in a freshly-built one.
	var activeClients []ppdmclient.Client
	var activeCancel context.CancelFunc
	stopActive := func() {
		if activeCancel != nil {
			activeCancel()
		}
		for _, c := range activeClients {
			_ = c.Close()
		}
	}
	defer stopActive()

	startLoop := func(c *config.Config) {
		clients := buildClients(c)
		col := ppdm.NewCollector(clients, ppdm.Registry(c.Collection.Lookback, c.Collection.AssetAgeThreshold),
			store, c.Collection.Interval, c.Collection.Timeout)
		log.Info("running collection cycle")
		col.CollectOnce(ctx)
		if otlpExp != nil {
			if err := otlpExp.EnsureInstruments(); err != nil {
				log.WithError(err).Warn("OTLP instrument registration failed")
			}
		}
		lctx, cancel := context.WithCancel(ctx)
		activeClients, activeCancel = clients, cancel
		if !once {
			go col.Run(lctx)
		}
	}

	startLoop(cfg)
	if once {
		return nil
	}

	if w, werr := config.NewWatcher(cfgPath); werr != nil {
		log.WithError(werr).Warn("config hot-reload disabled")
	} else {
		defer func() { _ = w.Close() }()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case ncfg := <-w.Updates():
					stopActive()
					startLoop(ncfg)
					log.Info("config reloaded; collector rebuilt")
				}
			}
		}()
	}

	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

// buildClients constructs a live PPDM client per configured server.
func buildClients(cfg *config.Config) []ppdmclient.Client {
	clients := make([]ppdmclient.Client, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		clients = append(clients, ppdmclient.NewServerClient(ppdmclient.Config{
			Name: s.Name, BaseURL: s.BaseURL(), Username: s.Username,
			Password: s.Password, InsecureSkipVerify: s.InsecureSkipVerify,
		}))
	}
	return clients
}

func healthHandler(w http.ResponseWriter, store *ppdm.SnapshotStore) {
	snap := store.Load()
	type serverHealth struct {
		Server     string `json:"server"`
		OK         bool   `json:"ok"`
		LastScrape string `json:"last_scrape"`
		Err        string `json:"err,omitempty"`
	}
	out := struct {
		BuiltAt string         `json:"built_at"`
		Servers []serverHealth `json:"servers"`
	}{BuiltAt: snap.BuiltAt.Format(time.RFC3339)}
	healthy := len(snap.Servers) > 0
	for _, s := range snap.Servers {
		out.Servers = append(out.Servers, serverHealth{s.Server, s.OK, s.LastScrape.Format(time.RFC3339), s.Err})
		if !s.OK {
			healthy = false
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(out)
}
