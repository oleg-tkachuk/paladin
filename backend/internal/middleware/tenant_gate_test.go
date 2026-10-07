package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// fakeStates answers from a map and counts its reads.
type fakeStates struct {
	states map[uuid.UUID]auth.TenantState
	err    error
	reads  int
}

func (f *fakeStates) TenantState(_ context.Context, id uuid.UUID) (auth.TenantState, error) {
	f.reads++
	if f.err != nil {
		return 0, f.err
	}
	return f.states[id], nil
}

func callThroughGate(t *testing.T, states auth.TenantStateReader, p *auth.Principal) (called bool, err error) {
	t.Helper()
	ctx := context.Background()
	if p != nil {
		ctx = auth.WithPrincipal(ctx, p)
	}
	next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return nil, nil
	})
	_, err = TenantGate(states).WrapUnary(next)(ctx, nil)
	return called, err
}

func reasonOf(err error) string {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return ""
	}
	for _, d := range cerr.Details() {
		if v, verr := d.Value(); verr == nil {
			if info, ok := v.(*errdetails.ErrorInfo); ok {
				return info.GetReason()
			}
		}
	}
	return ""
}

// Moving a tenant to the trash revoked nothing, so its API tokens,
// capabilities and sessions went on working. The gate refuses them.
func TestTenantGateRefusesATenantThatCannotActNow(t *testing.T) {
	live, trashed, gone := uuid.New(), uuid.New(), uuid.New()
	states := &fakeStates{states: map[uuid.UUID]auth.TenantState{
		live: auth.TenantLive, trashed: auth.TenantTrashed, gone: auth.TenantMissing,
	}}
	for _, tc := range []struct {
		name   string
		p      *auth.Principal
		code   connect.Code // 0: passes
		reason string
	}{
		{"a live tenant's principal", &auth.Principal{TenantID: live}, 0, ""},
		{"no principal, as Login has", nil, 0, ""},
		{"a principal with no tenant", &auth.Principal{Roles: []string{apiutil.RolePlatformAdmin}}, 0, ""},
		{"a platform admin of a trashed tenant", &auth.Principal{TenantID: trashed, Roles: []string{apiutil.RolePlatformAdmin}}, 0, ""},
		{"a tenant provisioner of a tenant still to be created", &auth.Principal{TenantID: gone, Roles: []string{apiutil.RoleTenantProvisioner}}, 0, ""},
		{"a tenant admin of a trashed tenant", &auth.Principal{TenantID: trashed, Roles: []string{apiutil.RoleTenantAdmin}}, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED.String()},
		{"a trashed tenant's principal", &auth.Principal{TenantID: trashed}, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED.String()},
		{"a purged tenant's principal", &auth.Principal{TenantID: gone}, connect.CodeUnauthenticated, commonv1.ErrorReason_ERROR_REASON_UNAUTHENTICATED.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called, err := callThroughGate(t, states, tc.p)
			if tc.code == 0 {
				if err != nil || !called {
					t.Fatalf("called=%v err=%v, want the call through", called, err)
				}
				return
			}
			if called || connect.CodeOf(err) != tc.code || reasonOf(err) != tc.reason {
				t.Fatalf("called=%v err=%v reason=%q, want %v with %s", called, err, reasonOf(err), tc.code, tc.reason)
			}
		})
	}
}

// A state the gate cannot read refuses the call as retryable, and keeps the
// cause out of what the caller is told.
func TestTenantGateFailsClosed(t *testing.T) {
	states := &fakeStates{err: errors.New(`ERROR: relation "tenants" does not exist (SQLSTATE 42P01)`)}
	called, err := callThroughGate(t, states, &auth.Principal{TenantID: uuid.New()})
	if called || connect.CodeOf(err) != connect.CodeUnavailable || strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatalf("called=%v err=%v, want Unavailable without the cause", called, err)
	}
}

func TestCachedTenantStatesHoldsUntilTTLOrClear(t *testing.T) {
	id := uuid.New()
	reader := &fakeStates{states: map[uuid.UUID]auth.TenantState{id: auth.TenantLive}}
	cache := NewCachedTenantStates(reader, time.Minute)
	now := time.Unix(0, 0)
	cache.now = func() time.Time { return now }
	read := func() auth.TenantState {
		t.Helper()
		s, err := cache.TenantState(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	read()
	reader.states[id] = auth.TenantTrashed
	if got := read(); got != auth.TenantLive || reader.reads != 1 {
		t.Fatalf("within the TTL: %v after %d reads, want the cached live state", got, reader.reads)
	}
	cache.Clear()
	if got := read(); got != auth.TenantTrashed {
		t.Fatalf("after Clear: %v, want the trashed state read again", got)
	}
	reader.states[id] = auth.TenantLive
	now = now.Add(time.Minute)
	if got := read(); got != auth.TenantLive {
		t.Fatalf("past the TTL: %v, want the state read again", got)
	}
}

func TestCachedTenantStatesDoesNotKeepAFailedRead(t *testing.T) {
	id := uuid.New()
	reader := &fakeStates{err: errors.New("down")}
	cache := NewCachedTenantStates(reader, time.Minute)
	if _, err := cache.TenantState(context.Background(), id); err == nil {
		t.Fatal("a failed read succeeded")
	}
	reader.err, reader.states = nil, map[uuid.UUID]auth.TenantState{id: auth.TenantLive}
	if s, err := cache.TenantState(context.Background(), id); err != nil || s != auth.TenantLive {
		t.Fatalf("after recovery: %v, %v", s, err)
	}
}
