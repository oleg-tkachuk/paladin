package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
)

var errRefused = errors.New("connect: connection refused")

// fast keeps the tests in milliseconds; the shape of the policy is what is
// under test, not its production durations.
var fast = postgres.Wait{
	Budget:         200 * time.Millisecond,
	FirstDelay:     time.Millisecond,
	MaxDelay:       4 * time.Millisecond,
	AttemptTimeout: 50 * time.Millisecond,
}

// pingFailing fails its first n calls, then answers.
func pingFailing(n int) (func(context.Context) error, *int) {
	calls := 0
	return func(context.Context) error {
		calls++
		if calls <= n {
			return errRefused
		}
		return nil
	}, &calls
}

func observed() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func TestWaitReady_AnswersAtOnce(t *testing.T) {
	ping, calls := pingFailing(0)
	log, logs := observed()

	require.NoError(t, postgres.WaitReady(t.Context(), ping, fast, log))
	assert.Equal(t, 1, *calls)
	assert.Zero(t, logs.FilterLevelExact(zapcore.WarnLevel).Len(), "no retry, no warning")
}

func TestWaitReady_RetriesUntilTheDatabaseAnswers(t *testing.T) {
	ping, calls := pingFailing(3)
	log, logs := observed()

	require.NoError(t, postgres.WaitReady(t.Context(), ping, fast, log))
	assert.Equal(t, 4, *calls)

	retries := logs.FilterMessage("database not ready; retrying").AllUntimed()
	require.Len(t, retries, 3, "every failed attempt says so")
	assert.Equal(t, int64(1), retries[0].ContextMap()["attempt"])
	assert.Equal(t, errRefused.Error(), retries[0].ContextMap()["error"])

	ready := logs.FilterMessage("database ready").AllUntimed()
	require.Len(t, ready, 1)
	assert.Equal(t, int64(4), ready[0].ContextMap()["attempts"])
}

func TestWaitReady_GivesUpWhenTheBudgetRunsOut(t *testing.T) {
	ping, calls := pingFailing(1 << 30)
	log, _ := observed()

	start := time.Now()
	err := postgres.WaitReady(t.Context(), ping, fast, log)

	require.ErrorIs(t, err, errRefused, "the last cause reaches the caller")
	require.ErrorContains(t, err, "database not ready after")
	assert.Greater(t, *calls, 1)
	assert.Less(t, time.Since(start), fast.Budget+fast.AttemptTimeout+fast.MaxDelay*4,
		"stops near the budget, not after it")
}

func TestWaitReady_StopsWhenTheCallerCancels(t *testing.T) {
	ping, _ := pingFailing(1 << 30)
	log, _ := observed()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := postgres.WaitReady(ctx, ping, postgres.Wait{
		Budget: time.Hour, FirstDelay: time.Hour, MaxDelay: time.Hour, AttemptTimeout: time.Second,
	}, log)

	require.ErrorIs(t, err, context.Canceled)
}

func TestWaitReady_BoundsEachAttempt(t *testing.T) {
	// A dial that hangs must not spend the whole budget on one attempt.
	attempts := 0
	hang := func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	log, _ := observed()

	require.NoError(t, postgres.WaitReady(t.Context(), hang, fast, log))
	assert.Equal(t, 2, attempts)
}

func TestWaitReady_DelaysGrowAndCap(t *testing.T) {
	ping, _ := pingFailing(5)
	log, logs := observed()

	require.NoError(t, postgres.WaitReady(t.Context(), ping, fast, log))

	var delays []time.Duration
	for _, e := range logs.FilterMessage("database not ready; retrying").AllUntimed() {
		delays = append(delays, time.Duration(e.ContextMap()["retry_in"].(time.Duration)))
	}
	assert.Equal(t, []time.Duration{
		time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond,
	}, delays)
}
