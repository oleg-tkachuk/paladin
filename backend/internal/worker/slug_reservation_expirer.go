package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// SlugReservationExpirerName names the job in its tick metrics and logs.
const SlugReservationExpirerName = "slug_reservation_expirer"

// SlugReservationExpirer releases the slug of a purged tenant once
// admin.tenants.slug_reservation_period has passed since its purge, so the
// retired list holds only slugs still reserved. A nil Period keeps every
// slug until an operator releases it, and the job does nothing.
type SlugReservationExpirer struct {
	Store    RetiredSlugExpirer
	Period   *time.Duration
	Interval time.Duration
	Logger   *zap.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// ExpiredSlug is a slug whose reservation ended, and the tenant that held it.
type ExpiredSlug struct {
	Slug           string
	FormerTenantID string
	RetiredAt      time.Time
}

// RetiredSlugExpirer is the store seam: release every slug retired at or
// before retiredBefore, and say which.
type RetiredSlugExpirer interface {
	ExpireRetiredSlugs(ctx context.Context, retiredBefore time.Time) ([]ExpiredSlug, error)
}

// Run ticks until ctx is done.
func (e *SlugReservationExpirer) Run(ctx context.Context) error {
	if e.Period == nil {
		e.log().Info("slug reservation period unset; retired slugs stay reserved until released")
		return nil
	}
	if e.Interval <= 0 || e.Store == nil {
		e.log().Warn("slug reservation expirer disabled: no interval or store",
			zap.Duration("interval", e.Interval))
		return nil
	}
	e.log().Info("slug reservation expirer started",
		zap.Duration("period", *e.Period), zap.Duration("interval", e.Interval))
	return RunTicker(ctx, SlugReservationExpirerName, e.Interval, e.tick)
}

func (e *SlugReservationExpirer) tick(ctx context.Context) error {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	cutoff := now().Add(-*e.Period)
	expired, err := e.Store.ExpireRetiredSlugs(ctx, cutoff)
	if err != nil {
		e.log().Error("failed to release expired tenant slugs", zap.Time("retired_before", cutoff), zap.Error(err))
		return err
	}
	for _, s := range expired {
		e.log().Info("tenant slug reservation expired, slug released",
			zap.String("slug", s.Slug),
			zap.String("former_tenant_id", s.FormerTenantID),
			zap.Time("retired_at", s.RetiredAt))
	}
	return nil
}

func (e *SlugReservationExpirer) log() *zap.Logger {
	if e.Logger == nil {
		return zap.NewNop()
	}
	return e.Logger
}
