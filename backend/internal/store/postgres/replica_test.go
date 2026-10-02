package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// fakeDB is a sqlc.DBTX that only identifies itself; Read never calls it.
type fakeDB struct{ name string }

func (fakeDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (fakeDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) { return nil, nil }
func (fakeDB) QueryRow(context.Context, string, ...interface{}) pgx.Row        { return nil }

func dur(d time.Duration) *time.Duration { return &d }

func testRouter(lag func() (*time.Duration, error), maxLag time.Duration) (*ReadRouter, fakeDB, fakeDB) {
	primary, replica := fakeDB{"primary"}, fakeDB{"replica"}
	r := &ReadRouter{
		primary: primary,
		replica: replica,
		lag:     func(context.Context) (*time.Duration, error) { return lag() },
		maxLag:  maxLag,
		period:  time.Second,
		log:     zap.NewNop(),
	}
	return r, primary, replica
}

// servedBy runs one Read and reports which database it landed on.
func servedBy(t *testing.T, r *ReadRouter) string {
	t.Helper()
	var got string
	if err := r.Read(context.Background(), func(db sqlc.DBTX) error {
		got = db.(fakeDB).name
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	return got
}

func TestReadRouter_PrimaryOnly(t *testing.T) {
	r := NewPrimaryOnlyRouter(fakeDB{"primary"})
	if r.HasReplica() || r.InSync() {
		t.Fatal("a primary-only router reports a replica")
	}
	if got := servedBy(t, r); got != "primary" {
		t.Fatalf("served by %s", got)
	}
	r.Run(context.Background()) // must return at once, not block
}

func TestReadRouter_NilIsPrimaryOnly(t *testing.T) {
	var r *ReadRouter
	if r.HasReplica() || r.InSync() {
		t.Fatal("nil router reports a replica")
	}
	r.Run(context.Background())
}

// An unprobed replica is not trusted: a standby still being seeded must not
// serve a read before anyone has measured it.
func TestReadRouter_UntrustedUntilProbed(t *testing.T) {
	r, _, _ := testRouter(func() (*time.Duration, error) { return dur(0), nil }, time.Second)
	if got := servedBy(t, r); got != "primary" {
		t.Fatalf("unprobed replica served the read")
	}
	r.probe(context.Background())
	if got := servedBy(t, r); got != "replica" {
		t.Fatalf("in-sync replica not used; served by %s", got)
	}
}

// The whole point of the probe: reads follow the replica's lag both ways
// with no operator action — catching up moves them over, falling behind or
// going away moves them back.
func TestReadRouter_FollowsLag(t *testing.T) {
	var lag *time.Duration
	var lagErr error
	r, _, _ := testRouter(func() (*time.Duration, error) { return lag, lagErr }, 2*time.Second)

	steps := []struct {
		name string
		lag  *time.Duration
		err  error
		want string
	}{
		{"catching up", dur(time.Minute), nil, "primary"},
		{"caught up", dur(500 * time.Millisecond), nil, "replica"},
		{"at the bound", dur(2 * time.Second), nil, "replica"},
		{"fell behind", dur(3 * time.Second), nil, "primary"},
		{"back", dur(0), nil, "replica"},
		{"unknown lag", nil, nil, "primary"},
		{"back again", dur(0), nil, "replica"},
		{"unreachable", nil, errors.New("connection refused"), "primary"},
		{"recovered", dur(0), nil, "replica"},
	}
	for _, s := range steps {
		lag, lagErr = s.lag, s.err
		r.probe(context.Background())
		if got := servedBy(t, r); got != s.want {
			t.Fatalf("%s: served by %s, want %s", s.name, got, s.want)
		}
	}
}

func TestReadRouter_ZeroMaxLagIsUnbounded(t *testing.T) {
	r, _, _ := testRouter(func() (*time.Duration, error) { return dur(time.Hour), nil }, 0)
	r.probe(context.Background())
	if got := servedBy(t, r); got != "replica" {
		t.Fatalf("max_lag 0 should not bound lag; served by %s", got)
	}
}

// A replica error between two probes (a query cancelled by WAL replay, a
// standby restart) costs a retry on the primary, not a failed request.
func TestReadRouter_RetriesOnPrimary(t *testing.T) {
	r, _, _ := testRouter(func() (*time.Duration, error) { return dur(0), nil }, time.Second)
	r.probe(context.Background())

	var calls []string
	err := r.Read(context.Background(), func(db sqlc.DBTX) error {
		name := db.(fakeDB).name
		calls = append(calls, name)
		if name == "replica" {
			return errors.New("canceling statement due to conflict with recovery")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(calls) != 2 || calls[0] != "replica" || calls[1] != "primary" {
		t.Fatalf("calls = %v, want [replica primary]", calls)
	}
}

func TestReadRouter_NoRetryOnCancelOrNoRows(t *testing.T) {
	r, _, _ := testRouter(func() (*time.Duration, error) { return dur(0), nil }, time.Second)
	r.probe(context.Background())

	for name, tc := range map[string]struct {
		ctx context.Context
		err error
	}{
		"no rows":   {context.Background(), pgx.ErrNoRows},
		"cancelled": {cancelled(), errors.New("context canceled")},
	} {
		calls := 0
		err := r.Read(tc.ctx, func(sqlc.DBTX) error { calls++; return tc.err })
		if !errors.Is(err, tc.err) || calls != 1 {
			t.Errorf("%s: calls = %d, err = %v; want one call returning the error", name, calls, err)
		}
	}
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
