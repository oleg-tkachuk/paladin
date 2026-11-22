package objecth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const (
	updCollection = "docs"
	updKey        = "reports/q1.pdf"
	updOtherKey   = "reports/q2.pdf"
	updBackend    = "backend-1"
	updBucket     = "bucket-1"
	updVersion    = int64(7)
)

// scriptedUpdateRepo serves one object to UpdateObject and records what the
// update asked of it; each err field fails the matching call.
type scriptedUpdateRepo struct {
	fakeObjectRepo
	obj       Object
	findErr   error
	bucketErr error
	writeErr  error
	txErr     error // what RunInTx returns once the body has succeeded

	finds  int
	writes int
	args   UpdateMetadataArgs
}

func (r *scriptedUpdateRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	r.finds++
	if r.findErr != nil {
		return Object{}, r.findErr
	}
	return r.obj, nil
}

func (r *scriptedUpdateRepo) LookupBucket(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
	if r.bucketErr != nil {
		return "", "", r.bucketErr
	}
	return updBackend, updBucket, nil
}

func (r *scriptedUpdateRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if err := fn(ctx, nil); err != nil {
		return err
	}
	return r.txErr
}

func (r *scriptedUpdateRepo) UpdateMetadataTx(_ context.Context, _ pgx.Tx, args UpdateMetadataArgs) (Object, error) {
	r.writes++
	r.args = args
	return r.obj, r.writeErr
}

func updObject(tenantID uuid.UUID, state statemachine.State) Object {
	return Object{
		ObjectID: uuid.New(), TenantID: tenantID, Collection: updCollection, Key: updKey,
		ContentType: "application/pdf", SizeBytes: 42, Tags: map[string]string{"class": "pii"},
		State: state, ResourceVersion: updVersion,
	}
}

func updInput(obj Object) UpdateObjectInput {
	return UpdateObjectInput{
		Collection: updCollection, ObjectID: obj.ObjectID.String(), ResourceVersion: updVersion,
		UpdatedFields: []string{FieldMetadata, FieldTags, FieldExternalRef},
		Metadata:      map[string]string{"owner": "finance"},
		Tags:          map[string]string{"class": "public"},
		ContentType:   "text/plain",
		ExternalRef:   "ext-1",
	}
}

func updCtx(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
}

func wantUpdateCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if got := connect.CodeOf(err); err == nil || got != want {
		t.Fatalf("err = %v (code %v), want code %v", err, got, want)
	}
}

// A refused update never reaches the store: no caller, a request that does
// not name an object, or an object that cannot be found each stop before the
// write.
func TestUpdateObjectRefusesBeforeTheWrite(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	for name, tc := range map[string]struct {
		ctx       context.Context
		in        UpdateObjectInput
		findErr   error
		want      connect.Code
		wantFinds int
	}{
		"no caller":      {context.Background(), updInput(obj), nil, connect.CodeUnauthenticated, 0},
		"no collection":  {updCtx(tenantID), UpdateObjectInput{ObjectID: obj.ObjectID.String()}, nil, connect.CodeInvalidArgument, 0},
		"no object id":   {updCtx(tenantID), UpdateObjectInput{Collection: updCollection}, nil, connect.CodeInvalidArgument, 0},
		"not an id":      {updCtx(tenantID), UpdateObjectInput{Collection: updCollection, ObjectID: "not-a-uuid"}, nil, connect.CodeInvalidArgument, 0},
		"no such object": {updCtx(tenantID), updInput(obj), ErrObjectNotFound, connect.CodeNotFound, 1},
		"lookup failing": {updCtx(tenantID), updInput(obj), errors.New("connection reset"), connect.CodeInternal, 1},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &scriptedUpdateRepo{obj: obj, findErr: tc.findErr}
			authz := &recordingAuthorizer{}
			h := &Handler{repo: repo, policy: authz}
			got, err := h.UpdateObject(tc.ctx, tc.in)
			wantUpdateCode(t, err, tc.want)
			if got != nil {
				t.Errorf("a refused update returned %+v", got)
			}
			if repo.finds != tc.wantFinds || repo.writes != 0 || authz.lastResource != nil {
				t.Errorf("finds = %d, writes = %d, authorized = %v; want %d finds, no write, no authz",
					repo.finds, repo.writes, authz.lastResource != nil, tc.wantFinds)
			}
		})
	}
}

// A capability gates the update on OpPut against the object it names: one
// that may only read, or that is pinned to another object, is refused before
// Cedar or the store hear of it.
func TestUpdateObjectChecksTheCapability(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	uri := func(key string) string { return paladin.ObjectResource(tenantID.String(), updCollection, key) }
	for name, tc := range map[string]struct {
		caveats limes.Caveats
		refused bool
	}{
		"may put the object":    {limes.Caveats{Ops: []limes.Op{limes.OpPut}, ResourceURIs: []string{uri(updKey)}}, false},
		"may only read":         {limes.Caveats{Ops: []limes.Op{limes.OpGet}}, true},
		"pinned to another key": {limes.Caveats{Ops: []limes.Op{limes.OpPut}, ResourceURIs: []string{uri(updOtherKey)}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &scriptedUpdateRepo{obj: obj}
			authz := &recordingAuthorizer{}
			h := &Handler{repo: repo, policy: authz}
			ctx := auth.WithCapability(updCtx(tenantID), captest.Verified(t, limes.Capability{
				Subject: limes.Principal{TenantID: tenantID}, Caveats: tc.caveats,
			}))
			_, err := h.UpdateObject(ctx, updInput(obj))
			if !tc.refused {
				if err != nil {
					t.Fatalf("UpdateObject: %v", err)
				}
				return
			}
			wantUpdateCode(t, err, connect.CodePermissionDenied)
			if repo.writes != 0 || authz.lastResource != nil {
				t.Errorf("a refused capability still reached authz (%v) or wrote %d times", authz.lastResource != nil, repo.writes)
			}
		})
	}
}

// Cedar refusing the update leaves the object untouched.
func TestUpdateObjectCedarDenial(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	repo := &scriptedUpdateRepo{obj: obj}
	h := &Handler{repo: repo, policy: denyAll{}}
	_, err := h.UpdateObject(updCtx(tenantID), updInput(obj))
	wantUpdateCode(t, err, connect.CodePermissionDenied)
	if repo.writes != 0 {
		t.Errorf("a denied update wrote %d times", repo.writes)
	}
}

// An update is a write: failing to resolve its bucket binding fails it, rather
// than authorizing it on an empty binding and writing, as it used to.
func TestUpdateObjectFailsClosedOnAnUnresolvableBucket(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	repo := &scriptedUpdateRepo{obj: obj, bucketErr: errors.New("connection reset")}
	authz := &recordingAuthorizer{}
	h := &Handler{repo: repo, policy: authz}
	_, err := h.UpdateObject(updCtx(tenantID), updInput(obj))
	wantUpdateCode(t, err, connect.CodeInternal)
	if authz.lastResource != nil {
		t.Fatalf("Cedar was asked with %+v; an unresolved write must not reach it", authz.lastResource)
	}
	if repo.writes != 0 {
		t.Errorf("writes = %d, want 0", repo.writes)
	}
}

// Cedar sees the object as stored, in the collection's binding, and the
// request context carries the object's size and type.
func TestUpdateObjectAuthorizesTheStoredObject(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	authz := &recordingAuthorizer{}
	h := &Handler{repo: &scriptedUpdateRepo{obj: obj}, policy: authz}
	if _, err := h.UpdateObject(updCtx(tenantID), updInput(obj)); err != nil {
		t.Fatalf("UpdateObject: %v", err)
	}
	want := cedar.Resource{
		TenantID: tenantID, Collection: updCollection, Key: updKey,
		BackendID: updBackend, BucketName: updBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}
	if authz.lastAction != cedar.ActionUpdateObject || authz.lastResource == nil || !reflect.DeepEqual(*authz.lastResource, want) {
		t.Fatalf("authorized %q on %+v, want %q on %+v", authz.lastAction, authz.lastResource, cedar.ActionUpdateObject, want)
	}
}

// The refusal of a non-AVAILABLE object names the state it is in.
func TestUpdateObjectNamesTheRefusedState(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateDeleted)
	h := &Handler{repo: &scriptedUpdateRepo{obj: obj}, policy: allowAll{}}
	_, err := h.UpdateObject(updCtx(tenantID), updInput(obj))
	wantUpdateCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), string(statemachine.StateDeleted)) {
		t.Errorf("err = %v, want it to name state %s", err, statemachine.StateDeleted)
	}
}

// The write and its event share one transaction: an event that cannot be
// recorded fails the update, and so does a commit that fails after both.
func TestUpdateObjectTransactionFailures(t *testing.T) {
	tenantID := uuid.New()
	for name, tc := range map[string]struct {
		eventErr, txErr error
	}{
		"event not recorded": {eventErr: errors.New("outbox insert failed")},
		"commit failed":      {txErr: errors.New("commit failed")},
	} {
		t.Run(name, func(t *testing.T) {
			obj := updObject(tenantID, statemachine.StateAvailable)
			repo := &scriptedUpdateRepo{obj: obj, txErr: tc.txErr}
			h := &Handler{repo: repo, policy: allowAll{}, events: &fakeEvents{err: tc.eventErr}}
			got, err := h.UpdateObject(updCtx(tenantID), updInput(obj))
			wantUpdateCode(t, err, connect.CodeInternal)
			if got != nil {
				t.Errorf("a failed update returned %+v", got)
			}
		})
	}
}

// A stale version is the caller's to retry; the store's sentinel keeps its
// code through the handler.
func TestUpdateObjectStaleVersion(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	h := &Handler{repo: &scriptedUpdateRepo{obj: obj, writeErr: ErrVersionMismatch}, policy: allowAll{}}
	_, err := h.UpdateObject(updCtx(tenantID), updInput(obj))
	wantUpdateCode(t, err, connect.CodeAborted)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("err = %v, want it to wrap ErrVersionMismatch", err)
	}
}

// A successful update writes exactly what the request asked for, returns
// the stored object, and records paladin.object.updated with the mask.
func TestUpdateObjectWritesTheRequest(t *testing.T) {
	tenantID := uuid.New()
	obj := updObject(tenantID, statemachine.StateAvailable)
	in := updInput(obj)
	repo := &scriptedUpdateRepo{obj: obj}
	events := &fakeEvents{}
	h := &Handler{repo: repo, policy: allowAll{}, events: events}

	got, err := h.UpdateObject(updCtx(tenantID), in)
	if err != nil {
		t.Fatalf("UpdateObject: %v", err)
	}
	if got == nil || !reflect.DeepEqual(*got, obj) {
		t.Errorf("returned %+v, want the stored object %+v", got, obj)
	}
	wantArgs := UpdateMetadataArgs{
		TenantID: tenantID, ObjectID: obj.ObjectID, ResourceVersion: in.ResourceVersion,
		UpdatedFields: in.UpdatedFields, Metadata: in.Metadata, Tags: in.Tags,
		ContentType: in.ContentType, ExternalRef: in.ExternalRef,
	}
	if repo.writes != 1 || !reflect.DeepEqual(repo.args, wantArgs) {
		t.Errorf("wrote %d times with %+v, want once with %+v", repo.writes, repo.args, wantArgs)
	}
	if len(events.evts) != 1 {
		t.Fatalf("events = %d, want 1", len(events.evts))
	}
	evt := events.evts[0]
	if evt.Type != eventtype.ObjectUpdated || !reflect.DeepEqual(evt.Payload[payloadUpdatedFields], in.UpdatedFields) {
		t.Errorf("event = %s with %v, want %s with the mask %v", evt.Type, evt.Payload, eventtype.ObjectUpdated, in.UpdatedFields)
	}
}
