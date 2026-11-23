//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/notify"
)

// notifyTestChannel is the channel this test listens on; no production
// watcher uses it.
const notifyTestChannel = "notify_watch_reconnect_test"

// notifyReconnectTimeout bounds how long the watcher may take to listen again
// after its connection is killed: its backoff starts at 100ms.
const notifyReconnectTimeout = 20 * time.Second

// TestNotifyWatchReconnect drops the connection a notify.Watcher listens on
// and proves it listens again, says so in the log, and treats the gap as a
// change.
func TestNotifyWatchReconnect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := startPostgres(t)
	core, logs := observer.New(zap.InfoLevel)
	ctx = logger.WithContext(ctx, zap.New(core))

	notified := make(chan struct{}, 1)
	w := notify.New(pool, notifyTestChannel, func() {
		select {
		case notified <- struct{}{}:
		default:
		}
	})
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	killListenerOf(t, ctx, pool, "LISTEN "+notifyTestChannel)

	select {
	case <-notified:
	case <-time.After(notifyReconnectTimeout):
		t.Fatalf("no notification within %s of the drop", notifyReconnectTimeout)
	}
	if n := w.Reconnects(); n < 1 {
		t.Fatalf("Reconnects = %d, want >= 1", n)
	}
	assertLogged(t, logs, notify.MsgDropped)
	assertLogged(t, logs, notify.MsgListening)
	for _, e := range logs.FilterMessage(notify.MsgListening).All() {
		if e.ContextMap()["channel"] != notifyTestChannel {
			t.Fatalf("listening line names channel %v, want %q", e.ContextMap()["channel"], notifyTestChannel)
		}
	}
}
