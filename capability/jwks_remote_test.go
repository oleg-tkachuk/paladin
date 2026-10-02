package capability

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// jwksServer serves a mutable key set with an ETag and counts fetches.
type jwksServer struct {
	mu      sync.Mutex
	keys    map[string]ed25519.PublicKey
	fail    bool
	fetches atomic.Int32
}

func (s *jwksServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.fetches.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	doc, err := MarshalJWKS(s.keys)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag := `"` + string(rune('a'+len(s.keys))) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	_, _ = w.Write(doc)
}

func (s *jwksServer) set(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newRemoteFixture(t *testing.T) (*jwksServer, *RemoteJWKSResolver, *stepClock, string, ed25519.PublicKey) {
	t.Helper()
	kid, pub, _, err := GenerateEd25519Keypair()
	if err != nil {
		t.Fatal(err)
	}
	srv := &jwksServer{keys: map[string]ed25519.PublicKey{kid: pub}}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	clock := &stepClock{t: time.Unix(1_800_000_000, 0)}
	r, err := NewRemoteJWKSResolver(RemoteJWKSConfig{
		URL: ts.URL, Client: ts.Client(), Now: clock.Now,
		RefreshInterval: time.Minute, MinRefreshInterval: 10 * time.Second, MaxStale: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, r, clock, kid, pub
}

func TestRemoteJWKSCachesUntilRefreshInterval(t *testing.T) {
	srv, r, clock, kid, pub := newRemoteFixture(t)
	ctx := context.Background()

	for range 3 {
		got, err := r.PublicKey(ctx, kid)
		if err != nil || !got.Equal(pub) {
			t.Fatalf("PublicKey = %v, %v", got, err)
		}
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1 (cached)", n)
	}

	clock.Advance(2 * time.Minute)
	if _, err := r.PublicKey(ctx, kid); err != nil {
		t.Fatal(err)
	}
	if n := srv.fetches.Load(); n != 2 {
		t.Fatalf("fetches after refresh interval = %d, want 2", n)
	}
}

// A new kid is picked up on first sight — that is how rotation reaches a
// remote verifier — but unknown kids cannot force a fetch per token.
func TestRemoteJWKSPicksUpRotatedKeyAndRateLimitsUnknownKids(t *testing.T) {
	srv, r, clock, kid, _ := newRemoteFixture(t)
	ctx := context.Background()
	if _, err := r.PublicKey(ctx, kid); err != nil {
		t.Fatal(err)
	}

	for range 5 {
		if _, err := r.PublicKey(ctx, "made-up"); !errors.Is(err, ErrUnknownKID) {
			t.Fatalf("unknown kid err = %v, want ErrUnknownKID", err)
		}
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1: unknown kids inside MinRefreshInterval must not refetch", n)
	}

	newKid, newPub, _, _ := GenerateEd25519Keypair()
	srv.set(func() { srv.keys[newKid] = newPub })
	clock.Advance(11 * time.Second)
	got, err := r.PublicKey(ctx, newKid)
	if err != nil || !got.Equal(newPub) {
		t.Fatalf("rotated-in key = %v, %v", got, err)
	}
}

// Through an outage the last good set keeps verifying, up to MaxStale; past
// it the resolver fails closed.
func TestRemoteJWKSServesStaleThenFailsClosed(t *testing.T) {
	srv, r, clock, kid, _ := newRemoteFixture(t)
	ctx := context.Background()
	if _, err := r.PublicKey(ctx, kid); err != nil {
		t.Fatal(err)
	}
	srv.set(func() { srv.fail = true })

	clock.Advance(30 * time.Minute)
	if _, err := r.PublicKey(ctx, kid); err != nil {
		t.Fatalf("within MaxStale: %v, want the cached key", err)
	}

	clock.Advance(31 * time.Minute)
	if _, err := r.PublicKey(ctx, kid); !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("past MaxStale err = %v, want ErrJWKSUnavailable", err)
	}

	srv.set(func() { srv.fail = false })
	clock.Advance(time.Minute)
	if _, err := r.PublicKey(ctx, kid); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
}

func TestRemoteJWKSNeverReachedFailsClosed(t *testing.T) {
	r, err := NewRemoteJWKSResolver(RemoteJWKSConfig{URL: "http://127.0.0.1:1/jwks.json"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PublicKey(context.Background(), "any"); !errors.Is(err, ErrJWKSUnavailable) {
		t.Fatalf("err = %v, want ErrJWKSUnavailable", err)
	}
}
