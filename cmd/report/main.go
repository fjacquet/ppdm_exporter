// Command report captures PowerProtect Data Manager backup history into PostgreSQL
// for assurance reporting (durable history; Grafana + branded reports read it).
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/ppdmclient"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/delivery"
	"github.com/fjacquet/ppdm_exporter/internal/report/render"
	"github.com/fjacquet/ppdm_exporter/internal/report/schedule"
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
	root.AddCommand(renderCommand())
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

	if cfg.Report.Listen != "" {
		scopes := make([]render.TokenScope, 0, len(cfg.Report.Tokens))
		for _, t := range cfg.Report.Tokens {
			scopes = append(scopes, render.TokenScope{Token: t.Token, Tenants: t.Tenants})
		}
		authz := render.NewAuthorizer(cfg.Report.AuthToken, scopes)
		h := render.NewHandler(store, cfg.Report.BrandName, authz)
		srv := &http.Server{Addr: cfg.Report.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.WithField("addr", cfg.Report.Listen).Info("serving report endpoint")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.WithError(err).Error("report endpoint failed")
			}
		}()
		defer func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutCtx); err != nil {
				log.WithError(err).Warn("report endpoint shutdown")
			}
		}()
	}

	if !once && len(cfg.Schedules) > 0 {
		deliverer, derr := delivery.NewSMTP(cfg.SMTP)
		if derr != nil {
			return derr
		}
		sched := schedule.New(store, deliverer, cfg.Schedules, cfg.Report.BrandName)
		go sched.Run(ctx)
		log.WithField("schedules", len(cfg.Schedules)).Info("report scheduler started")
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

func renderCommand() *cobra.Command {
	var cfgPath, tenant, format, out string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render a tenant's backup-assurance report (html or pdf) to a file",
		RunE: func(_ *cobra.Command, _ []string) error {
			ext, err := render.FormatExt(format)
			if err != nil {
				return err
			}
			if tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			cfg, err := config.LoadReport(cfgPath)
			if err != nil {
				return err
			}
			ctx := context.Background()
			store, err := report.New(ctx, cfg.Database.DSN)
			if err != nil {
				return err
			}
			defer store.Close()
			data, err := render.Build(ctx, store, tenant, cfg.Report.BrandName, time.Now())
			if err != nil {
				return err
			}
			var w io.Writer = os.Stdout
			if out != "" {
				f, err := os.Create(out)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				w = f
			}
			if ext == "pdf" {
				return render.RenderPDF(w, data)
			}
			return render.RenderHTML(w, data)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "config.report.yaml", "path to config file")
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant to report on (required)")
	_ = cmd.MarkFlagRequired("tenant")
	cmd.Flags().StringVar(&format, "format", "html", "output format: html or pdf")
	cmd.Flags().StringVar(&out, "out", "", "output file (default: stdout)")
	return cmd
}
