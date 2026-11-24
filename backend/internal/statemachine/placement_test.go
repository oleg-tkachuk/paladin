package statemachine

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
)

// bindingAnswer answers the placement check's read of the collection's
// binding: its backend and bucket, or err.
type bindingAnswer struct {
	backend, bucket string
	err             error
}

func (b bindingAnswer) Scan(dest ...any) error {
	if b.err != nil {
		return b.err
	}
	*dest[0].(*string) = b.backend
	*dest[1].(*string) = b.bucket
	return nil
}

// The placements the tests confirm bytes in.
var (
	placedSource = Placement{BackendID: "be-1", Bucket: "shared"}
	placedTarget = Placement{BackendID: "be-1", Bucket: "dedicated"}
)

// A promote names the bucket its caller confirmed the bytes in, and is
// refused when a storage migration has rebound the collection since: the
// object would be read from a bucket that does not hold its bytes, and the
// migration's cleanup would delete the only copy.
func TestPromoteChecksTheCollectionIsStillBoundWhereTheBytesAre(t *testing.T) {
	cases := []struct {
		name    string
		placed  *Placement
		bound   bindingAnswer
		moved   bool
		readErr bool
	}{
		{"still bound there", &placedSource, bindingAnswer{backend: "be-1", bucket: "shared"}, false, false},
		{"rebound to another bucket", &placedSource, bindingAnswer{backend: "be-1", bucket: "dedicated"}, true, false},
		{"rebound to another backend", &placedTarget, bindingAnswer{backend: "be-2", bucket: "dedicated"}, true, false},
		{"the binding cannot be read", &placedSource, bindingAnswer{err: errors.New("conn reset")}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := &Transitioner{}
			ex := &fakeExec{row: fakeRow{state: string(StateAvailable)}, bindingRow: tc.bound}
			changed, err := sm.promote(smCtx, ex, objID, tc.placed, "etag", 1, "", "", SourceRPC)
			switch {
			case tc.moved:
				var bm *BindingMovedError
				if !errors.Is(err, ErrBindingMoved) || !errors.As(err, &bm) {
					t.Fatalf("err = %v, want ErrBindingMoved", err)
				}
				if bm.Placed != *tc.placed || bm.Bound != (Placement{BackendID: tc.bound.backend, Bucket: tc.bound.bucket}) {
					t.Errorf("refusal names %+v, want the placed and the bound bucket", bm)
				}
			case tc.readErr:
				if err == nil || errors.Is(err, ErrBindingMoved) {
					t.Fatalf("err = %v, want the read failure", err)
				}
			default:
				if err != nil || !changed {
					t.Fatalf("changed=%v err=%v, want a promote", changed, err)
				}
				return
			}
			if changed || ex.querySQL != "" {
				t.Fatal("a refused promote still ran the UPDATE")
			}
		})
	}
}

// Every promote holds the tenant's migration row in the states that rebind,
// whether or not it names a placement: the fence is what keeps a rebind from
// passing a promote in flight. One that names none reads no binding.
func TestPromoteHoldsTheMigrationFence(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{row: fakeRow{state: string(StateAvailable)}}
	if _, err := sm.promote(smCtx, ex, objID, nil, "etag", 1, "", "seq-1", SourceEvent); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !strings.Contains(ex.execSQL, "FOR SHARE") || !strings.Contains(ex.execSQL, "tenant_storage_migrations") {
		t.Fatalf("fence = %q, want the migration row held FOR SHARE", ex.execSQL)
	}
	states, ok := ex.execArgs[1].([]string)
	if !ok || !slices.Equal(states, migstate.FencingPromotes()) {
		t.Errorf("fence states = %v, want %v", ex.execArgs[1], migstate.FencingPromotes())
	}
	if ex.bindingCalls != 0 {
		t.Error("a promote naming no placement read the binding")
	}
}

// A fence that cannot be taken fails the promote: going on without it would
// let a rebind pass the promote unseen.
func TestPromoteFailsWithoutTheFence(t *testing.T) {
	sm := &Transitioner{}
	boom := errors.New("lock timeout")
	ex := &fakeExec{row: fakeRow{state: string(StateAvailable)}, execErr: boom}
	changed, err := sm.promote(smCtx, ex, objID, &placedSource, "etag", 1, "", "", SourceRPC)
	if !errors.Is(err, boom) || changed {
		t.Fatalf("changed=%v err=%v, want the fence's failure", changed, err)
	}
	if ex.querySQL != "" {
		t.Fatal("the UPDATE ran without the fence")
	}
}

// The states a promote is fenced in are the ones whose step moves a binding
// or stamps the copy's start; the others are not, so a migration at rest
// costs a promote nothing.
func TestFencingPromotesCoversEveryStepThatRebinds(t *testing.T) {
	got := migstate.FencingPromotes()
	for _, want := range []string{migstate.Provisioning, migstate.Copying, migstate.Rebinding, migstate.Verifying, migstate.RollingBack} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not fenced", want)
		}
	}
	for _, rest := range []string{migstate.Completed, migstate.Cleaned, migstate.Failed} {
		if slices.Contains(got, rest) {
			t.Errorf("%s is fenced, though nothing there moves a binding", rest)
		}
	}
}
