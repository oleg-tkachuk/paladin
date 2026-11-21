package objecth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// statefulUpdateRepo serves one object to UpdateObject and answers its write
// with err, counting the writes it was asked for.
type statefulUpdateRepo struct {
	updateRepo
	err    error
	writes int
}

func (r *statefulUpdateRepo) UpdateMetadataTx(_ context.Context, _ pgx.Tx, _ UpdateMetadataArgs) (Object, error) {
	r.writes++
	return r.obj, r.err
}

// A request names its object by collection and id; either missing, or an id
// that is not one, is the caller's mistake.
func TestObjectRef(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name, collection, objectID string
		want                       connect.Code
	}{
		{"no collection", "", id.String(), connect.CodeInvalidArgument},
		{"no object id", "docs", "", connect.CodeInvalidArgument},
		{"not an id", "docs", "not-a-uuid", connect.CodeInvalidArgument},
	} {
		if _, err := objectRef(tc.collection, tc.objectID); connect.CodeOf(err) != tc.want {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if got, err := objectRef("docs", id.String()); err != nil || got != id {
		t.Errorf("valid ref = (%v, %v), want (%v, nil)", got, err, id)
	}
}

// Only an AVAILABLE object takes a metadata update. One in the trash or still
// uploading matched no row and came back as a version conflict, which told the
// caller to re-read and retry an update that could never land.
func TestUpdateObjectOutcomes(t *testing.T) {
	tenantID := uuid.New()
	for _, tc := range []struct {
		name       string
		state      statemachine.State
		writeErr   error
		want       connect.Code
		wantWrites int
	}{
		{"available", statemachine.StateAvailable, nil, 0, 1},
		{"in the trash", statemachine.StateDeleted, nil, connect.CodeFailedPrecondition, 0},
		{"still uploading", statemachine.StatePending, nil, connect.CodeFailedPrecondition, 0},
		{"stale version", statemachine.StateAvailable, ErrVersionMismatch, connect.CodeAborted, 1},
		{"store down", statemachine.StateAvailable, errors.New("connection reset"), connect.CodeInternal, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := Object{
				ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "a.txt", State: tc.state,
			}
			repo := &statefulUpdateRepo{updateRepo: updateRepo{obj: obj}, err: tc.writeErr}
			h := &Handler{repo: repo, policy: &recordingAuthorizer{}}
			ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

			_, err := h.UpdateObject(ctx, UpdateObjectInput{
				Collection: "docs", ObjectID: obj.ObjectID.String(), UpdatedFields: []string{"tags"},
			})
			if got := connect.CodeOf(err); err != nil && got != tc.want || err == nil && tc.want != 0 {
				t.Fatalf("err = %v (code %v), want code %v", err, got, tc.want)
			}
			if repo.writes != tc.wantWrites {
				t.Errorf("writes = %d, want %d", repo.writes, tc.wantWrites)
			}
		})
	}
}
