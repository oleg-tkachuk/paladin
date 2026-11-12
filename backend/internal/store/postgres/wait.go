package postgres

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Wait is how long a caller waits for Postgres to answer before giving up,
// and how it spaces its attempts: the first retry after FirstDelay, each
// next one twice as late, never later than MaxDelay. AttemptTimeout bounds
// one attempt, so a dial that hangs does not spend the whole Budget.
type Wait struct {
	Budget         time.Duration
	FirstDelay     time.Duration
	MaxDelay       time.Duration
	AttemptTimeout time.Duration
}

// OneShotWait is the wait of the migrate and bootstrap commands. They run as
// Helm hooks, where one failure fails the whole release, so a database that
// is restarting for a minute or two must not fail them. Each Job's
// activeDeadlineSeconds has to cover this budget and the work after it; a
// test in cmd/server holds the chart's defaults to that.
var OneShotWait = Wait{
	Budget:         2 * time.Minute,
	FirstDelay:     time.Second,
	MaxDelay:       15 * time.Second,
	AttemptTimeout: 5 * time.Second,
}

// WaitReady calls ping until it succeeds, the budget runs out, or ctx ends.
// Every failed attempt is logged with its cause and the delay before the
// next one; giving up returns the last cause wrapped.
func WaitReady(ctx context.Context, ping func(context.Context) error, w Wait, log *zap.Logger) error {
	deadline := time.Now().Add(w.Budget)
	delay := w.FirstDelay
	for attempt := 1; ; attempt++ {
		err := pingOnce(ctx, ping, w.AttemptTimeout)
		if err == nil {
			log.Info("database ready", zap.Int("attempts", attempt))
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("waiting for the database: %w", ctx.Err())
		}
		if time.Now().Add(delay).After(deadline) {
			return fmt.Errorf("database not ready after %d attempts in %s: %w", attempt, w.Budget, err)
		}
		log.Warn("database not ready; retrying",
			zap.Int("attempt", attempt),
			zap.Duration("retry_in", delay),
			zap.Error(err))
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the database: %w", ctx.Err())
		case <-time.After(delay):
		}
		delay = min(delay*2, w.MaxDelay)
	}
}

func pingOnce(ctx context.Context, ping func(context.Context) error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ping(ctx)
}
