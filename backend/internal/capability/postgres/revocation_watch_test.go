package postgres

import (
	"errors"
	"log/slog"
	"testing"
)

// The watcher logs through the logger it is given, and still calls onError:
// a nil Logger would send its lines to slog.Default instead of the service's.
func TestNewRevocationWatcherLogsThroughTheGivenLogger(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	var heard error
	w := NewRevocationWatcher(nil, log, func(err error) { heard = err })
	if w.Logger != log {
		t.Fatalf("Logger = %p, want the given %p", w.Logger, log)
	}
	if w.Connect == nil {
		t.Fatal("Connect is nil")
	}
	want := errors.New("dropped")
	w.OnError(want)
	if !errors.Is(heard, want) {
		t.Fatalf("onError heard %v, want %v", heard, want)
	}
}

// Without onError the watcher has none: its failures are logged once, by
// pgstore through Logger.
func TestNewRevocationWatcherWithoutOnError(t *testing.T) {
	if w := NewRevocationWatcher(nil, slog.New(slog.DiscardHandler), nil); w.OnError != nil {
		t.Fatal("OnError set without one given")
	}
}
