// Command report captures PowerProtect Data Manager backup history into PostgreSQL
// for assurance reporting (durable history; Grafana + branded reports read it).
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	var cfgPath string
	var once, debug bool
	root := &cobra.Command{
		Use:     "report",
		Short:   "Capture PPDM backup history into PostgreSQL for assurance reporting",
		Version: version,
		RunE:    func(_ *cobra.Command, _ []string) error { return run(cfgPath, once, debug) },
	}
	root.Flags().StringVar(&cfgPath, "config", "config.report.yaml", "path to config file")
	root.Flags().BoolVar(&once, "once", false, "run a single capture cycle and exit")
	root.Flags().BoolVar(&debug, "debug", false, "verbose logging")
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(cfgPath string, once, debug bool) error {
	if debug {
		log.SetLevel(log.DebugLevel)
	}
	cfg, err := config.LoadReport(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := report.New(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	servers := make([]report.ServerClient, 0, len(cfg.Servers))
	var closers []ppdmclient.Client
	for _, s := range cfg.Servers {
		client := ppdmclient.NewServerClient(ppdmclient.Config{
			Name: s.Name, BaseURL: s.BaseURL(), Username: s.Username,
			Password: s.Password, InsecureSkipVerify: s.InsecureSkipVerify,
		})
		servers = append(servers, report.NewServerClient(s.Tenant, client))
		closers = append(closers, client)
	}
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	capt := report.NewCapturer(store, version, cfg.Capture.RetentionDays, cfg.Compliance)
	log.Info("running initial capture cycle")
	capt.RunOnce(ctx, servers, cfg.Capture.Timeout)
	if once {
		return nil
	}
	capt.Run(ctx, servers, cfg.Capture.Interval, cfg.Capture.Timeout)
	return nil
}
