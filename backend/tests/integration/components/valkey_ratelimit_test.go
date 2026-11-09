//go:build integration

// The rate-limit window in a real Valkey (ADR-0046). The unit suite pins the
// window's arithmetic on a fake clock; this pins the Lua script that repeats
// it on the server's clock, and the properties only a shared store has: two
// replicas count against one ceiling, and concurrent callers cannot both take
// the last slot.
//
// Requires Docker; run via `task backend:test:integration` (tags=integration).
package components

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	valkeygo "github.com/valkey-io/valkey-go"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	rcvalkey "github.com/oleg-tkachuk/paladin/backend/internal/ratecount/valkey"
	"github.com/oleg-tkachuk/paladin/backend/tests/testimage"
)

// valkeyCallTimeout is generous for a container on the same host; the
// unreachable case below sets its own.
const valkeyCallTimeout = 2 * time.Second

// valkeyKeyPrefix mirrors the package's key prefix, to inspect a key.
const valkeyKeyPrefix = "paladin:ratelimit:"

// startValkey runs a Valkey and returns its address.
func startValkey(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if err := testimage.Ensure(ctx, testimage.Valkey); err != nil {
		t.Fatalf("ensure image: %v", err)
	}
	c, err := tcvalkey.Run(ctx, testimage.Valkey)
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	raw, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Host
}

func dialValkey(t *testing.T, addr string, timeout time.Duration) valkeygo.Client {
	t.Helper()
	c, err := rcvalkey.Dial(rcvalkey.Options{Addr: addr, Timeout: timeout})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// awayFromRollover waits until the current bucket has room for a test that
// must not straddle two buckets.
func awayFromRollover(t *testing.T, c ratecount.Counter, key string, need time.Duration) {
	t.Helper()
	for {
		got, err := c.Read(context.Background(), key)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		left := ratecount.Window - got.Elapsed
		if left > need {
			return
		}
		time.Sleep(left + time.Second)
	}
}

func TestValkeyWindow(t *testing.T) {
	addr := startValkey(t)
	ctx := context.Background()

	t.Run("two replicas count against one ceiling", func(t *testing.T) {
		a := rcvalkey.New(dialValkey(t, addr, valkeyCallTimeout), valkeyCallTimeout)
		b := rcvalkey.New(dialValkey(t, addr, valkeyCallTimeout), valkeyCallTimeout)
		const key = "tenant:replicas"
		awayFromRollover(t, a, key, 10*time.Second)
		for i := 0; i < 3; i++ {
			if _, err := a.Bump(ctx, key); err != nil {
				t.Fatal(err)
			}
		}
		got, err := b.Bump(ctx, key)
		if err != nil || got.Current != 4 || !got.Admitted {
			t.Fatalf("second replica saw %+v %v", got, err)
		}
	})

	t.Run("concurrent callers cannot share the last slot", func(t *testing.T) {
		const capacity, callers = 50, 200
		c := rcvalkey.New(dialValkey(t, addr, valkeyCallTimeout), valkeyCallTimeout)
		const key = "token:concurrent"
		awayFromRollover(t, c, key, 20*time.Second)
		var admitted atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := c.BumpIfFits(ctx, key, capacity)
				if err != nil {
					t.Error(err)
					return
				}
				if got.Admitted {
					admitted.Add(1)
				}
			}()
		}
		wg.Wait()
		if admitted.Load() != capacity {
			t.Fatalf("admitted %d, want exactly %d", admitted.Load(), capacity)
		}
	})

	t.Run("the script weighs the previous bucket as Go does", func(t *testing.T) {
		const capacity, previous = 100, 100
		client := dialValkey(t, addr, valkeyCallTimeout)
		c := rcvalkey.New(client, valkeyCallTimeout)
		const key = "token:weighted"
		awayFromRollover(t, c, key, 10*time.Second)
		probe, err := c.Read(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		index := probe.BucketStart.UnixMicro() / ratecount.Window.Microseconds()
		if err := client.Do(ctx, client.B().Hset().Key(valkeyKeyPrefix+key).
			FieldValue().FieldValue(itoa(index-1), itoa(previous)).Build()).Error(); err != nil {
			t.Fatalf("seed previous bucket: %v", err)
		}
		refused := false
		for i := 0; i < capacity && !refused; i++ {
			got, err := c.BumpIfFits(ctx, key, capacity)
			if err != nil {
				t.Fatal(err)
			}
			before := got.Current
			if got.Admitted {
				before--
			}
			want := ratelimit.Weighted(before+1, got.Previous, got.Elapsed) <= capacity
			if got.Previous != previous || got.Admitted != want {
				t.Fatalf("call %d: %+v, Go decides admitted=%v", i, got, want)
			}
			refused = !got.Admitted
		}
		if !refused {
			t.Fatal("never refused with the previous bucket full")
		}
	})

	t.Run("a read counts nothing and the key expires", func(t *testing.T) {
		client := dialValkey(t, addr, valkeyCallTimeout)
		c := rcvalkey.New(client, valkeyCallTimeout)
		const key = "tenant:read"
		awayFromRollover(t, c, key, 10*time.Second)
		if _, err := c.Bump(ctx, key); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			got, err := c.Read(ctx, key)
			if err != nil || got.Current != 1 || got.Admitted {
				t.Fatalf("read %d: %+v %v", i, got, err)
			}
		}
		ttl, err := client.Do(ctx, client.B().Pttl().Key(valkeyKeyPrefix+key).Build()).AsInt64()
		if err != nil || ttl <= 0 || ttl > (2*ratecount.Window).Milliseconds() {
			t.Fatalf("pttl %d %v", ttl, err)
		}
	})

	t.Run("an unreachable store degrades to the process", func(t *testing.T) {
		const shortTimeout = 200 * time.Millisecond
		client := dialValkey(t, addr, shortTimeout)
		c := rcvalkey.New(client, shortTimeout)
		const key = "tenant:unreachable"
		if _, err := c.Bump(ctx, key); err != nil {
			t.Fatal(err)
		}
		// A paused server takes connections and answers nothing, the
		// failure a timeout is for.
		if err := client.Do(ctx, client.B().ClientPause().Timeout(int64((5 * time.Second).Milliseconds())).All().Build()).Error(); err != nil {
			t.Fatalf("pause: %v", err)
		}
		start := time.Now()
		if _, err := c.Bump(ctx, key); err == nil {
			t.Fatal("a paused server answered")
		}
		if waited := time.Since(start); waited > 4*shortTimeout {
			t.Fatalf("call waited %v past a %v timeout", waited, shortTimeout)
		}
		d := ratecount.NewDegrading(c, "valkey", zap.NewNop())
		got, err := d.BumpIfFits(ctx, key, 1)
		if err != nil || !got.Admitted {
			t.Fatalf("degraded call: %+v %v", got, err)
		}
		if got, _ := d.BumpIfFits(ctx, key, 1); got.Admitted {
			t.Fatal("the per-process fallback lifted the limit")
		}
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
