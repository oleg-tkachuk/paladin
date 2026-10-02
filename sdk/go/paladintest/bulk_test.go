package paladintest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// overlap is long enough that downloads started together overlap.
const overlap = 20 * time.Millisecond

func TestDownloadManyReadsEveryObjectAndReportsEachFailure(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	var names []string
	want := map[string]string{}
	for i := range 6 {
		o := srv.Put(srv.Collection(), fmt.Sprintf("k%d", i), "text/plain", []byte(fmt.Sprintf("body %d", i)))
		names = append(names, o.GetName())
		want[o.GetName()] = fmt.Sprintf("body %d", i)
	}
	missing := srv.Collection().String() + "/objects/00000000-0000-4000-8000-000000000000"
	var (
		mu  sync.Mutex
		got = map[string]string{}
	)
	failures := paladin.DownloadMany(context.Background(), p.Data, append(names, missing), 2,
		func(name string, r *paladin.ObjectReader) error {
			b, err := io.ReadAll(r)
			mu.Lock()
			defer mu.Unlock()
			got[name] = string(b)
			return err
		})
	for name, body := range want {
		if got[name] != body {
			t.Errorf("%s = %q, want %q", name, got[name], body)
		}
	}
	if len(failures) != 1 || !errors.Is(failures[missing], paladin.ErrNotFound) {
		t.Errorf("failures = %v, want only the missing object, as NotFound", failures)
	}
}

func TestDownloadManyKeepsToItsConcurrency(t *testing.T) {
	const limit = 3
	srv := paladintest.New(t)
	p := srv.Connect()
	var names []string
	for i := range 9 {
		names = append(names, srv.Put(srv.Collection(), fmt.Sprint(i), "text/plain", []byte("x")).GetName())
	}
	var active, peak atomic.Int32
	failures := paladin.DownloadMany(context.Background(), p.Data, names, limit, func(string, *paladin.ObjectReader) error {
		n := active.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(overlap)
		active.Add(-1)
		return nil
	})
	if failures != nil || peak.Load() != limit {
		t.Errorf("failures %v, peak %d; want none and %d at once", failures, peak.Load(), limit)
	}
}

func TestDownloadManyStopsOpeningWhenCancelled(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failures := paladin.DownloadMany(ctx, p.Data, []string{"a", "b"}, 1, func(string, *paladin.ObjectReader) error { return nil })
	if len(failures) != 2 {
		t.Errorf("failures = %v, want both names, cancelled or failed", failures)
	}
}
