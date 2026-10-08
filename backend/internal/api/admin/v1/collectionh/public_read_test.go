package collectionh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
)

// actionAuthorizer denies one action and allows the rest, recording what it
// was asked.
type actionAuthorizer struct {
	deny  cedar.Action
	asked []cedar.Action
}

func (a *actionAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.asked = append(a.asked, action)
	if action == a.deny {
		return cedar.DecisionDeny, nil
	}
	return cedar.DecisionAllow, nil
}

// echoRepo stores what it is given, as the real repository returns the row
// it wrote.
func echoRepo() *fakeRepo {
	return &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
		return Collection{
			TenantID: a.TenantID, Collection: a.Collection, BackendID: a.BackendID, BucketName: a.BucketName,
			PublicRead: a.PublicRead, CacheControl: a.CacheControl,
		}, nil
	}}
}

func publicArgs() CreateCollectionArgs {
	return CreateCollectionArgs{Collection: "photos", BackendID: "aws-eu", BucketName: "public", PublicRead: true}
}

// A public collection needs ConfigurePublicRead on top of ManageCollection,
// and is stored with the default Cache-Control when it asks for none.
func TestCreatePublicCollection(t *testing.T) {
	tid := uuid.New()
	repo := echoRepo()
	authz := &actionAuthorizer{}
	h := NewHandler(repo, authz)
	if _, err := h.CreateCollection(authedCtx(tid), publicArgs()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if !repo.lastCreate.PublicRead || repo.lastCreate.CacheControl != publicread.DefaultCacheControl {
		t.Errorf("stored %+v, want public with the default cache control", repo.lastCreate)
	}
	if !containsAction(authz.asked, cedar.ActionConfigurePublicRead) {
		t.Errorf("asked %v, want ConfigurePublicRead among them", authz.asked)
	}
}

func TestCreatePublicCollectionRefusals(t *testing.T) {
	tid := uuid.New()
	withCache := func(public bool, cc string) CreateCollectionArgs {
		a := publicArgs()
		a.PublicRead, a.CacheControl = public, cc
		return a
	}
	for name, tc := range map[string]struct {
		args CreateCollectionArgs
		deny cedar.Action
		code connect.Code
	}{
		"without ConfigurePublicRead":               {publicArgs(), cedar.ActionConfigurePublicRead, connect.CodePermissionDenied},
		"a private collection with a cache control": {withCache(false, "no-store"), cedar.Action{}, connect.CodeFailedPrecondition},
		"a cache control a header cannot carry":     {withCache(true, "a\r\nb"), cedar.Action{}, connect.CodeFailedPrecondition},
	} {
		t.Run(name, func(t *testing.T) {
			repo := echoRepo()
			h := NewHandler(repo, &actionAuthorizer{deny: tc.deny})
			_, err := h.CreateCollection(authedCtx(tid), tc.args)
			wantCode(t, err, tc.code)
			if repo.lastCreate.Collection != "" {
				t.Errorf("created %+v despite the refusal", repo.lastCreate)
			}
		})
	}
}

// A private collection never asks Cedar about publishing.
func TestCreatePrivateCollectionDoesNotAskToPublish(t *testing.T) {
	authz := &actionAuthorizer{}
	h := NewHandler(echoRepo(), authz)
	args := publicArgs()
	args.PublicRead = false
	if _, err := h.CreateCollection(authedCtx(uuid.New()), args); err != nil {
		t.Fatal(err)
	}
	if containsAction(authz.asked, cedar.ActionConfigurePublicRead) {
		t.Errorf("asked %v for a private collection", authz.asked)
	}
}

func TestEnsureCollectionNeverPublishes(t *testing.T) {
	h := NewHandler(&fakeRepo{}, allowAll())
	_, err := h.EnsureCollection(authedCtx(uuid.New()), publicArgs())
	wantCode(t, err, connect.CodeFailedPrecondition)
}

// A rebind the schema refuses as a public-collection rule reaches the caller
// as that rule, not as a generic failed rebind.
func TestRebindRefusedByAPublicRule(t *testing.T) {
	h := NewHandler(&fakeRepo{
		rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error {
			return publicread.Rulef("a public collection never moves")
		},
	}, allowAll())
	_, err := h.BindCollectionToBucket(authedCtx(uuid.New()), uuid.Nil, "photos", "storageBackends/aws-eu/buckets/other", 0)
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeFailedPrecondition || len(cerr.Details()) == 0 {
		t.Errorf("err = %v, want FailedPrecondition carrying the rule's reason", err)
	}
}

func containsAction(actions []cedar.Action, want cedar.Action) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}
