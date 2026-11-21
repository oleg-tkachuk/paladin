package objecth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// pathRepo answers a lookup by path with obj, or err.
type pathRepo struct {
	fakeObjectRepo
	obj Object
	err error
}

func (r *pathRepo) FindByPath(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.obj, r.err
}

// A lookup by path the store fails is an outage, not an absent object: every
// error used to be NotFound, so a database that was down read as a key with
// nothing at it.
func TestLookupObjectLookupFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want connect.Code
	}{
		"no such object":    {fmt.Errorf("look up: %w", ErrObjectNotFound), connect.CodeNotFound},
		"the store failing": {errors.New("connection refused"), connect.CodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			h := &Handler{repo: &pathRepo{err: tc.err}, policy: allowAll{}}
			_, err := h.LookupObject(purgeCtx(uuid.New()), "docs", "a.txt")
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("code = %v (%v), want %v", got, err, tc.want)
			}
		})
	}
}

// A HEAD that finds nothing means nothing was uploaded, which the caller can
// fix; a HEAD that could not reach the backend is not that, and was reported
// as "not uploaded yet" all the same.
func TestCompleteObjectHeadFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want connect.Code
	}{
		"nothing at the key":    {fmt.Errorf("head: %w", ErrStoredObjectNotFound), connect.CodeFailedPrecondition},
		"backend not reachable": {errors.New("dial tcp: connection refused"), connect.CodeUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			obj := Object{ObjectID: uuid.NewV7(), Collection: "docs", State: statemachine.StatePending}
			h, _, ctx, _ := completeHandler(&completeRepo{obj: obj}, headStorage{err: tc.err})
			_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()})
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("code = %v (%v), want %v", got, err, tc.want)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("the cause was dropped: %v", err)
			}
		})
	}
}

// stateUpdateRepo reads cur and counts the updates it was asked for.
type stateUpdateRepo struct {
	fakeObjectRepo
	cur       Object
	updateErr error
	updates   int
}

func (r *stateUpdateRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.cur, nil
}

func (r *stateUpdateRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}

func (r *stateUpdateRepo) UpdateMetadataTx(context.Context, pgx.Tx, UpdateMetadataArgs) (Object, error) {
	r.updates++
	return r.cur, r.updateErr
}

func TestAssertBypassAllowed(t *testing.T) {
	withRoles := func(roles ...string) *auth.Principal { return &auth.Principal{Subject: "u1", Roles: roles} }
	for name, tc := range map[string]struct {
		principal *auth.Principal
		bypass    bool
		want      connect.Code
	}{
		"no bypass asked":           {nil, false, 0},
		"no principal":              {nil, true, connect.CodePermissionDenied},
		"plain member":              {withRoles(apiutil.RoleTenantUser), true, connect.CodePermissionDenied},
		"governance bypass role":    {withRoles(RoleLockGovernanceBypass), true, 0},
		"platform admin":            {withRoles(apiutil.RolePlatformAdmin), true, 0},
		"plain member, no bypass":   {withRoles(apiutil.RoleTenantUser), false, 0},
		"bypass role among others":  {withRoles(apiutil.RoleTenantUser, RoleLockGovernanceBypass), true, 0},
		"admin of the tenant alone": {withRoles(apiutil.RoleTenantAdmin), true, connect.CodePermissionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			err := (&Handler{}).assertBypassAllowed(tc.principal, tc.bypass)
			if got := connect.CodeOf(err); err != nil && got != tc.want || err == nil && tc.want != 0 {
				t.Fatalf("err = %v, want code %v", err, tc.want)
			}
		})
	}
}
