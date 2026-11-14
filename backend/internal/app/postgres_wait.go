package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const (
	// postgresFirstRetry is the pause after the first refused ping at start;
	// each next one doubles, up to postgresMaxRetry, so a database back in
	// seconds is found in seconds and one down for minutes is not hammered.
	postgresFirstRetry = time.Second
	postgresMaxRetry   = 10 * time.Second
)

// sleepFunc waits d or until ctx ends, reporting whether the wait ran out.
type sleepFunc func(ctx context.Context, d time.Duration) bool

// waitForPostgres pings until Postgres answers or wait has passed, logging
// each refusal and, after any, the answer that ended them. A role that exits
// at the first refusal comes back under the kubelet's growing restart delay,
// and stays down minutes after the database is back; one that waits starts
// the moment it answers. With wait 0 it pings once.
func waitForPostgres(ctx context.Context, ping func(context.Context) error, wait time.Duration, log *zap.Logger, sleep sleepFunc) error {
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	start := time.Now()
	delay := postgresFirstRetry
	for attempt := 1; ; attempt++ {
		err := ping(ctx)
		if err == nil {
			if attempt > 1 {
				log.Info("postgres answered; starting",
					zap.Int("attempt", attempt), zap.Duration("waited", time.Since(start)))
			}
			return nil
		}
		gaveUp := func() error {
			return fmt.Errorf("app: postgres unreachable after %d attempts in %s: %w",
				attempt, time.Since(start).Round(time.Millisecond), err)
		}
		if wait <= 0 || ctx.Err() != nil {
			return gaveUp()
		}
		log.Warn("postgres unreachable at start; retrying",
			zap.Int("attempt", attempt), zap.Duration("retry_in", delay),
			zap.Duration("startup_wait", wait), zap.Error(err))
		if !sleep(ctx, delay) {
			return gaveUp()
		}
		delay = min(2*delay, postgresMaxRetry)
	}
}

// sleepOnClock is sleepFunc on the clock.
func sleepOnClock(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
