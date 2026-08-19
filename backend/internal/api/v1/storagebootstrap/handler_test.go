package storagebootstrap

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// ─── fakes ────────────────────────────────────────────────────────────────

// recordingAuthorizer allows/denies and records the (tenant, resource) it saw
// so a test can prove the handler authorizes against the CALLER's tenant.
type recordingAuthorizer struct {
	allow          bool
	gotResource    *cedar.Resource
	gotPrincipalTN uuid.UUID
	gotAction      string
}

func (a *recordingAuthorizer) IsAuthorized(_ context.Context, p *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.gotResource = r
	a.gotPrincipalTN = p.TenantID
	a.gotAction = action
	if a.allow {
		return cedar.DecisionAllow, nil
	}
	return cedar.DecisionDeny, nil
}

type fakeBackends struct {
	enabled bool
	err     error
	gotID   string
}

func (b *fakeBackends) BackendEnabled(_ context.Context, backendID string) (bool, error) {
	b.gotID = backendID
	return b.enabled, b.err
}

type fakeBuckets struct {
	created bool
	err     error
	calls   int
	gotIn   bucketh.CreateBucketInput
}

func (b *fakeBuckets) EnsureBucket(_ context.Context, in bucketh.CreateBucketInput) (*admindomain.Bucket, bool, error) {
	b.calls++
	b.gotIn = in
	if b.err != nil {
		return nil, false, b.err
	}
	return &admindomain.Bucket{BackendID: in.Bucket.BackendID, BucketName: in.Bucket.BucketName}, b.created, nil
}

// fakeObjectKeys reports "created" for keys in the created set, "existing"
// otherwise, and records the tenant each Create ran under.
type fakeObjectKeys struct {
	createdKeys map[string]bool
	err         error
	seenTenants []uuid.UUID
	seenKeys    []string
}

func (o *fakeObjectKeys) EnsureObjectKey(ctx context.Context, args objectkey.CreateObjectKeyArgs) (bool, error) {
	if o.err != nil {
		return false, o.err
	}
	// Mirror the real handler: tenant is the caller's, resolved from ctx.
	tid, _ := auth.TenantFromContext(ctx)
	o.seenTenants = append(o.seenTenants, tid)
	o.seenKeys = append(o.seenKeys, args.ObjectKey)
	return o.createdKeys[args.ObjectKey], nil
}

// ctxWithPAT builds a request context carrying an ApiKey principal bound to
// tenantID — exactly what the data-plane APITokenAuthInterceptor establishes.
func ctxWithPAT(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: tenantID,
		Subject:  "apikey:" + uuid.NewString(),
		Audience: auth.AudienceData,
		Kind:     auth.PrincipalKindApiKey,
	})
}

const (
	backendID = "garage-local"
	bucket    = "acme-consumer"
)

func newHandler(authz cedar.Authorizer, backends BackendChecker, buckets BucketEnsurer, keys ObjectKeyEnsurer) *Handler {
	return NewHandler(buckets, keys, backends, authz)
}

// ─── tests ────────────────────────────────────────────────────────────────

// Tenant is taken from the principal, never the request, and it is the tenant
// the handler authorizes + provisions under.
func TestEnsureTenantStorage_TenantFromContextNotRequest(t *testing.T) {
	caller := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000a1")
	authz := &recordingAuthorizer{allow: true}
	backends := &fakeBackends{enabled: true}
	buckets := &fakeBuckets{created: true}
	keys := &fakeObjectKeys{createdKeys: map[string]bool{"docs": true}}
	h := newHandler(authz, backends, buckets, keys)

	res, err := h.EnsureTenantStorage(ctxWithPAT(caller), backendID, bucket, []string{"docs"})
	if err != nil {
		t.Fatalf("EnsureTenantStorage: %v", err)
	}
	if authz.gotPrincipalTN != caller || authz.gotResource.TenantID != caller {
		t.Errorf("authorize used tenant principal=%s resource=%s, want caller %s",
			authz.gotPrincipalTN, authz.gotResource.TenantID, caller)
	}
	if authz.gotAction != cedar.ActionEnsureTenantStorage {
		t.Errorf("action = %q, want EnsureTenantStorage", authz.gotAction)
	}
	// The authz resource is the caller's TENANT only (not the bucket) — the
	// built-in permit gates on resource.tenant_id equality. backend/bucket flow
	// to the provisioning calls, not the authz resource.
	if authz.gotResource.BackendID != "" || authz.gotResource.BucketName != "" {
		t.Errorf("authz resource must carry no backend/bucket, got %q/%q",
			authz.gotResource.BackendID, authz.gotResource.BucketName)
	}
	// Object-key create ran under the caller's tenant.
	if len(keys.seenTenants) != 1 || keys.seenTenants[0] != caller {
		t.Errorf("object-key create tenants = %v, want [%s]", keys.seenTenants, caller)
	}
	// Shared bucket: OwnerTenantID must stay empty (uuid.Nil).
	if buckets.gotIn.Bucket.OwnerTenantID != uuid.Nil {
		t.Errorf("bucket OwnerTenantID = %s, want uuid.Nil (shared)", buckets.gotIn.Bucket.OwnerTenantID)
	}
	if !buckets.gotIn.ProvisionOnBackend {
		t.Error("EnsureBucket must request provision_on_backend=true")
	}
	if !res.BucketCreated {
		t.Error("BucketCreated = false, want true")
	}
	if len(res.ObjectKeysCreated) != 1 || res.ObjectKeysCreated[0] != "docs" {
		t.Errorf("ObjectKeysCreated = %v, want [docs]", res.ObjectKeysCreated)
	}
}

// Existing tenant/bucket/keys → all no-ops, success (idempotent).
func TestEnsureTenantStorage_IdempotentNoOp(t *testing.T) {
	caller := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000a2")
	buckets := &fakeBuckets{created: false} // already existed
	keys := &fakeObjectKeys{createdKeys: map[string]bool{}}
	h := newHandler(&recordingAuthorizer{allow: true}, &fakeBackends{enabled: true}, buckets, keys)

	res, err := h.EnsureTenantStorage(ctxWithPAT(caller), backendID, bucket, []string{"docs", "reports"})
	if err != nil {
		t.Fatalf("EnsureTenantStorage: %v", err)
	}
	if res.BucketCreated {
		t.Error("BucketCreated = true, want false (already existed)")
	}
	if len(res.ObjectKeysCreated) != 0 {
		t.Errorf("ObjectKeysCreated = %v, want none", res.ObjectKeysCreated)
	}
	if len(res.ObjectKeysExisting) != 2 {
		t.Errorf("ObjectKeysExisting = %v, want 2", res.ObjectKeysExisting)
	}
}

// Unknown backend → FailedPrecondition, and no bucket/key mutation happens.
func TestEnsureTenantStorage_UnknownBackend(t *testing.T) {
	caller := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000a3")
	backends := &fakeBackends{err: admindomain.ErrNotFound}
	buckets := &fakeBuckets{}
	h := newHandler(&recordingAuthorizer{allow: true}, backends, buckets, &fakeObjectKeys{})

	_, err := h.EnsureTenantStorage(ctxWithPAT(caller), "no-such-backend", bucket, []string{"docs"})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("err code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
	}
	if buckets.calls != 0 {
		t.Error("bucket must not be ensured when the backend is unknown")
	}
}

// Cedar deny → PermissionDenied, before any backend/bucket/key work.
func TestEnsureTenantStorage_CedarDeny(t *testing.T) {
	caller := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000a4")
	backends := &fakeBackends{enabled: true}
	buckets := &fakeBuckets{}
	h := newHandler(&recordingAuthorizer{allow: false}, backends, buckets, &fakeObjectKeys{})

	_, err := h.EnsureTenantStorage(ctxWithPAT(caller), backendID, bucket, []string{"docs"})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err code = %v, want PermissionDenied (err=%v)", connect.CodeOf(err), err)
	}
	if backends.gotID != "" || buckets.calls != 0 {
		t.Error("deny must short-circuit before backend/bucket work")
	}
}

// No principal → Unauthenticated.
func TestEnsureTenantStorage_NoPrincipal(t *testing.T) {
	h := newHandler(&recordingAuthorizer{allow: true}, &fakeBackends{enabled: true}, &fakeBuckets{}, &fakeObjectKeys{})
	_, err := h.EnsureTenantStorage(context.Background(), backendID, bucket, nil)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("err code = %v, want Unauthenticated (err=%v)", connect.CodeOf(err), err)
	}
}

// A bucket-ensure failure is surfaced (and object-keys are not touched).
func TestEnsureTenantStorage_BucketEnsureError(t *testing.T) {
	caller := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000a5")
	buckets := &fakeBuckets{err: connect.NewError(connect.CodeUnavailable, errors.New("provisioning not wired"))}
	keys := &fakeObjectKeys{createdKeys: map[string]bool{}}
	h := newHandler(&recordingAuthorizer{allow: true}, &fakeBackends{enabled: true}, buckets, keys)

	_, err := h.EnsureTenantStorage(ctxWithPAT(caller), backendID, bucket, []string{"docs"})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err code = %v, want Unavailable (err=%v)", connect.CodeOf(err), err)
	}
	if len(keys.seenKeys) != 0 {
		t.Error("object-keys must not be ensured after a bucket-ensure failure")
	}
}
