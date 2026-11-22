package objecth

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// The binding a healthy collection resolves to in these tests.
const (
	gateTestBackend = "backend-gate"
	gateTestBucket  = "bucket-gate"
)

// gatedRepo serves the object every RPC asks for and resolves its bucket
// through bucketgate.Check against state, as the store adapter does. lookupErr,
// when set, is the lookup itself failing instead.
type gatedRepo struct {
	unresolvedRepo
	state     bucketgate.State
	lookupErr error
}

func (r *gatedRepo) resolve(access bucketgate.Access) error {
	if r.lookupErr != nil {
		return r.lookupErr
	}
	return bucketgate.Check(access, r.state)
}

func (r *gatedRepo) LookupBucket(_ context.Context, _ uuid.UUID, _ string, access bucketgate.Access) (string, string, error) {
	if err := r.resolve(access); err != nil {
		return "", "", err
	}
	return gateTestBackend, gateTestBucket, nil
}

func (r *gatedRepo) LookupBucketMeta(_ context.Context, _ uuid.UUID, _ string, access bucketgate.Access) (BucketMeta, error) {
	if err := r.resolve(access); err != nil {
		return BucketMeta{}, err
	}
	return BucketMeta{BackendID: gateTestBackend, BucketName: gateTestBucket, ObjectLockEnabled: true}, nil
}

// denyRecorder records every Cedar check it is asked and denies it, so an RPC
// that passes the gate stops at its authorization with the binding recorded.
type denyRecorder struct {
	checks []cedar.Resource
}

func (a *denyRecorder) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.checks = append(a.checks, *r)
	return cedar.DecisionDeny, nil
}

// writeRPC calls one write RPC against repo with authz as its authorizer.
type writeRPC struct {
	name string
	call func(ctx context.Context, repo Repository, authz cedar.Authorizer, tenantID uuid.UUID) error
}

const gateTestCollection = "docs"

// gateTestRetention is how far ahead a test retention window ends.
const gateTestRetention = time.Hour

// writeRPCs is every write RPC of this package, each called the way a client
// would.
func writeRPCs() []writeRPC {
	objectID := uuid.New().String()
	handler := func(repo Repository, authz cedar.Authorizer) *Handler {
		return &Handler{repo: repo, storage: noopStorage{}, policy: authz, presign: testPresignConfig()}
	}
	return []writeRPC{
		{"UploadObject", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := handler(repo, authz).UploadObject(ctx, UploadObjectInput{
				Collection: gateTestCollection, Key: "a.txt", ContentType: "text/plain",
				ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue,
			})
			return err
		}},
		{"CompleteObject", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := handler(repo, authz).CompleteObject(ctx, CompleteObjectInput{Collection: gateTestCollection, ObjectID: objectID})
			return err
		}},
		{"CopyObject", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := handler(repo, authz).CopyObject(ctx, CopyObjectInput{
				SourceCollection: gateTestCollection, SourceObjectID: objectID,
				DestCollection: gateTestCollection, DestKey: "copy.txt",
			})
			return err
		}},
		{"UpdateObject", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := handler(repo, authz).UpdateObject(ctx, UpdateObjectInput{
				Collection: gateTestCollection, ObjectID: objectID, Tags: map[string]string{"k": "v"},
			})
			return err
		}},
		{"DeleteObject (trash)", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			return handler(repo, authz).DeleteObject(ctx, gateTestCollection, objectID, "1", false, false)
		}},
		{"DeleteObject (permanent)", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			return handler(repo, authz).DeleteObject(ctx, gateTestCollection, objectID, "1", true, false)
		}},
		{"RestoreObject", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := handler(repo, authz).RestoreObject(ctx, gateTestCollection, objectID, "1")
			return err
		}},
		{"RestoreVersion", func(ctx context.Context, repo Repository, authz cedar.Authorizer, tenantID uuid.UUID) error {
			h := NewVersionHandler(repo, newFakeVersionRepo())
			h.SetAuthorizer(authz)
			_, err := h.RestoreVersion(ctx, versionName(tenantID, uuid.NewV7(), uuid.NewV7()), "1")
			return err
		}},
		{"SetRetention", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := NewLockHandler(repo, nil, nil, authz).SetRetention(ctx, SetRetentionInput{
				Collection: gateTestCollection, ObjectID: objectID, Mode: LockModeGovernance, RetainUntil: time.Now().Add(gateTestRetention),
			})
			return err
		}},
		{"SetLegalHold", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := NewLockHandler(repo, nil, nil, authz).SetLegalHold(ctx, gateTestCollection, objectID, true)
			return err
		}},
		{"SetTaint", func(ctx context.Context, repo Repository, authz cedar.Authorizer, _ uuid.UUID) error {
			_, err := NewTaintHandler(repo, nil, authz).SetTaint(ctx, gateTestCollection, objectID, nil)
			return err
		}},
	}
}

// Every write RPC consults the same gate, so the same backend and bucket state
// answers every one of them the same way — and a write the gate refuses, or
// whose binding could not be read, never reaches Cedar: authorizing it with an
// empty binding is the fail-open this guards against.
func TestEveryWriteRPCAnswersTheBucketGateAlike(t *testing.T) {
	states := []struct {
		name string
		repo func() *gatedRepo
		want connect.Code
	}{
		{"draining", func() *gatedRepo {
			return &gatedRepo{state: bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}}
		}, connect.CodeFailedPrecondition},
		{"disabled", func() *gatedRepo {
			return &gatedRepo{state: bucketgate.State{ProvisionState: bucketgate.ProvisionReady}}
		}, connect.CodeFailedPrecondition},
		{"provisioning", func() *gatedRepo {
			return &gatedRepo{state: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}}
		}, connect.CodeUnavailable},
		{"provision failed", func() *gatedRepo {
			return &gatedRepo{state: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionFailed}}
		}, connect.CodeFailedPrecondition},
		{"store unreachable", func() *gatedRepo { return &gatedRepo{lookupErr: errBucketStore} }, connect.CodeInternal},
		{"collection missing", func() *gatedRepo {
			return &gatedRepo{lookupErr: fmt.Errorf("collection %q %w", gateTestCollection, apiutil.ErrNotFound)}
		}, connect.CodeNotFound},
	}
	for _, rpc := range writeRPCs() {
		for _, st := range states {
			t.Run(rpc.name+"/"+st.name, func(t *testing.T) {
				tenantID := uuid.New()
				ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
				authz := &denyRecorder{}
				err := rpc.call(ctx, st.repo(), authz, tenantID)
				if got := connect.CodeOf(err); got != st.want {
					t.Fatalf("%s = %v (code %v), want %v", rpc.name, err, got, st.want)
				}
				if len(authz.checks) != 0 {
					t.Fatalf("%s reached Cedar %d times with binding %+v; a refused write must not", rpc.name, len(authz.checks), authz.checks)
				}
			})
		}
	}
}

// Reading a lock is a read: a draining backend serves it. It used to resolve
// its bucket as a write and answer FailedPrecondition.
func TestGetLockIsServedWhileDraining(t *testing.T) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	authz := &denyRecorder{}
	repo := &gatedRepo{state: bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}}
	_, err := NewLockHandler(repo, nil, nil, authz).GetLock(ctx, gateTestCollection, uuid.New().String())
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied || len(authz.checks) != 1 {
		t.Fatalf("GetLock = %v (code %v, %d checks), want it past the gate to the recorder's denial", err, got, len(authz.checks))
	}
}

// On a healthy bucket every write RPC passes the gate and hands Cedar the
// binding it resolved, so a bucket- or collection-scoped token is decided on
// its own bucket.
func TestEveryWriteRPCAuthorizesOnTheResolvedBinding(t *testing.T) {
	for _, rpc := range writeRPCs() {
		t.Run(rpc.name, func(t *testing.T) {
			tenantID := uuid.New()
			ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
			authz := &denyRecorder{}
			repo := &gatedRepo{state: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}}
			err := rpc.call(ctx, repo, authz, tenantID)
			if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
				t.Fatalf("%s = %v (code %v), want the recorder's denial", rpc.name, err, got)
			}
			if len(authz.checks) == 0 {
				t.Fatalf("%s never reached Cedar", rpc.name)
			}
			if r := authz.checks[0]; r.BackendID != gateTestBackend || r.BucketName != gateTestBucket {
				t.Fatalf("%s authorized on binding (%q, %q), want (%q, %q)", rpc.name, r.BackendID, r.BucketName, gateTestBackend, gateTestBucket)
			}
		})
	}
}
