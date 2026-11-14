package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	// testStartupWait is long enough that only the fake's answers, never the
	// deadline, end a wait in these tests.
	testStartupWait = time.Hour
	// refusedPings is how many pings in a row the fake database refuses.
	refusedPings = 4
)

var errPostgresDown = errors.New("dial tcp: connect: connection refused")

// refusingThen refuses n pings, then answers.
func refusingThen(n int) (func(context.Context) error, *int) {
	calls := 0
	return func(context.Context) error {
		calls++
		if calls <= n {
			return errPostgresDown
		}
		return nil
	}, &calls
}

// recordedSleeps is a sleepFunc that returns at once and keeps each delay.
func recordedSleeps() (sleepFunc, *[]time.Duration) {
	var delays []time.Duration
	return func(_ context.Context, d time.Duration) bool {
		delays = append(delays, d)
		return true
	}, &delays
}

// A database that answers after a few refusals is waited for: each refusal
// logged with the pause before the next try, the pauses doubling to their
// ceiling, and the answer logged once with how many tries it took.
func TestWaitForPostgresRidesOutRefusals(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	ping, calls := refusingThen(refusedPings)
	sleep, delays := recordedSleeps()
	if err := waitForPostgres(t.Context(), ping, testStartupWait, zap.New(core), sleep); err != nil {
		t.Fatalf("waitForPostgres = %v, want the database found", err)
	}
	if *calls != refusedPings+1 {
		t.Errorf("pinged %d times, want %d", *calls, refusedPings+1)
	}
	want := []time.Duration{postgresFirstRetry, 2 * postgresFirstRetry, 4 * postgresFirstRetry, 8 * postgresFirstRetry}
	for i := range want {
		want[i] = min(want[i], postgresMaxRetry)
	}
	if len(*delays) != len(want) {
		t.Fatalf("paused %v, want %v", *delays, want)
	}
	for i, d := range *delays {
		if d != want[i] {
			t.Errorf("pause %d = %s, want %s", i, d, want[i])
		}
	}
	refused := logs.FilterMessage("postgres unreachable at start; retrying").AllUntimed()
	if len(refused) != refusedPings {
		t.Fatalf("%d refusals logged, want %d", len(refused), refusedPings)
	}
	for i, e := range refused {
		f := e.ContextMap()
		if e.Level != zapcore.WarnLevel || f["attempt"] != int64(i+1) || f["retry_in"] != want[i] || f["error"] != errPostgresDown.Error() {
			t.Errorf("refusal %d logged %v at %v", i, f, e.Level)
		}
	}
	answered := logs.FilterMessage("postgres answered; starting").AllUntimed()
	if len(answered) != 1 || answered[0].ContextMap()["attempt"] != int64(refusedPings+1) {
		t.Errorf("answer logged %v, want once at attempt %d", answered, refusedPings+1)
	}
}

// The pause between pings stops growing at its ceiling.
func TestWaitForPostgresCapsThePause(t *testing.T) {
	const manyRefusals = 8
	ping, _ := refusingThen(manyRefusals)
	sleep, delays := recordedSleeps()
	if err := waitForPostgres(t.Context(), ping, testStartupWait, zap.NewNop(), sleep); err != nil {
		t.Fatal(err)
	}
	if last := (*delays)[len(*delays)-1]; last != postgresMaxRetry {
		t.Errorf("last pause %s, want the ceiling %s", last, postgresMaxRetry)
	}
}

// A database that answers at once is no event: nothing is logged.
func TestWaitForPostgresQuietWhenItAnswers(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	ping, calls := refusingThen(0)
	sleep, delays := recordedSleeps()
	if err := waitForPostgres(t.Context(), ping, testStartupWait, zap.New(core), sleep); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || len(*delays) != 0 || logs.Len() != 0 {
		t.Errorf("calls %d, pauses %v, %d lines: want one quiet ping", *calls, *delays, logs.Len())
	}
}

// Past startup_wait the role gives up with the last refusal, so it exits and
// the kubelet restarts it; with startup_wait 0 it pings once.
func TestWaitForPostgresGivesUp(t *testing.T) {
	t.Run("the wait runs out", func(t *testing.T) {
		ping, _ := refusingThen(refusedPings)
		expired := func(context.Context, time.Duration) bool { return false }
		err := waitForPostgres(t.Context(), ping, testStartupWait, zap.NewNop(), expired)
		if !errors.Is(err, errPostgresDown) {
			t.Fatalf("err = %v, want the refusal", err)
		}
	})
	t.Run("no wait", func(t *testing.T) {
		ping, calls := refusingThen(refusedPings)
		sleep, delays := recordedSleeps()
		err := waitForPostgres(t.Context(), ping, 0, zap.NewNop(), sleep)
		if !errors.Is(err, errPostgresDown) || *calls != 1 || len(*delays) != 0 {
			t.Fatalf("err %v after %d pings and pauses %v, want one refused ping", err, *calls, *delays)
		}
	})
	t.Run("on the clock", func(t *testing.T) {
		const shortWait = 50 * time.Millisecond
		ping, _ := refusingThen(refusedPings)
		start := time.Now()
		err := waitForPostgres(t.Context(), ping, shortWait, zap.NewNop(), sleepOnClock)
		if !errors.Is(err, errPostgresDown) {
			t.Fatalf("err = %v, want the refusal", err)
		}
		if took := time.Since(start); took >= postgresFirstRetry {
			t.Errorf("gave up after %s, want within startup_wait %s", took, shortWait)
		}
	})
}
