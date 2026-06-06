package schedule

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/fjacquet/ppdm_exporter/internal/config"
	"github.com/fjacquet/ppdm_exporter/internal/report"
	"github.com/fjacquet/ppdm_exporter/internal/report/delivery"
	"github.com/fjacquet/ppdm_exporter/internal/report/render"
	log "github.com/sirupsen/logrus"
)

// Scheduler periodically generates and delivers each tenant's report on its cadence.
type Scheduler struct {
	store     *report.Store
	deliverer delivery.Deliverer
	schedules []config.Schedule
	brand     string
	tick      time.Duration
}

// New wires a scheduler. The tick (how often due-ness is re-checked) is 1 minute — fine for
// hour-granularity cadences.
func New(store *report.Store, d delivery.Deliverer, schedules []config.Schedule, brand string) *Scheduler {
	return &Scheduler{store: store, deliverer: d, schedules: schedules, brand: brand, tick: time.Minute}
}

// Run checks due-ness immediately, then every tick, until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	s.runDue(ctx, time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runDue(ctx, time.Now().UTC())
		}
	}
}

func (s *Scheduler) runDue(ctx context.Context, now time.Time) {
	for _, sc := range s.schedules {
		s.maybeSend(ctx, sc, now)
	}
}

// maybeSend delivers one tenant's report if it is due and not already delivered for the period.
// A panic, render error, or delivery error is contained to this tenant (logged + recorded) so it
// neither blocks other tenants nor kills the loop.
func (s *Scheduler) maybeSend(ctx context.Context, sc config.Schedule, now time.Time) {
	var period string
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"tenant": sc.Tenant, "panic": r}).Error("schedule send panicked")
			if period != "" {
				_ = s.store.RecordDelivery(ctx, sc.Tenant, period, false, fmt.Sprintf("panic: %v", r), sc.Recipients)
			}
		}
	}()
	if !Due(now, sc) {
		return
	}
	period = PeriodKey(now, sc)
	exists, err := s.store.DeliveryExists(ctx, sc.Tenant, period)
	if err != nil {
		log.WithError(err).WithField("tenant", sc.Tenant).Warn("delivery-exists check failed")
		return
	}
	if exists {
		return
	}
	record := func(ok bool, msg string) {
		if rerr := s.store.RecordDelivery(ctx, sc.Tenant, period, ok, msg, sc.Recipients); rerr != nil {
			log.WithError(rerr).WithField("tenant", sc.Tenant).Warn("record delivery failed")
		}
	}
	data, err := render.Build(ctx, s.store, sc.Tenant, s.brand, now)
	if err != nil {
		log.WithError(err).WithField("tenant", sc.Tenant).Info("skip delivery: no report data")
		record(false, err.Error())
		return
	}
	var html, pdf bytes.Buffer
	if err := render.RenderHTML(&html, data); err != nil {
		record(false, "render html: "+err.Error())
		return
	}
	if err := render.RenderPDF(&pdf, data); err != nil {
		record(false, "render pdf: "+err.Error())
		return
	}
	subject := fmt.Sprintf("Backup Assurance Report — %s — %s [%s]", sc.Tenant, period, data.BadgeText())
	derr := s.deliverer.Deliver(ctx, sc.Tenant, sc.Recipients, subject, html.Bytes(), pdf.Bytes())
	if derr != nil {
		log.WithError(derr).WithField("tenant", sc.Tenant).Warn("report delivery failed")
		record(false, derr.Error())
		return
	}
	log.WithFields(log.Fields{"tenant": sc.Tenant, "period": period}).Info("report delivered")
	record(true, "")
}
