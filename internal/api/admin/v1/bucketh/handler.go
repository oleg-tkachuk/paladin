// Package bucketh implements the admin BucketService — physical bucket
// CRUD + per-bucket policy / lifecycle / lock / versioning / replication.
package bucketh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// Cedar action names — must match policies/schema.cedarschema.
const (
	actionManageBucket = "ManageBucket"
	actionReadBucket   = "ReadBucket"
)

// Provisioner provisions/de-provisions the underlying S3 bucket. nil-safe:
// when not wired, CreateBucket(provision_on_backend=true) returns Unavailable.
type Provisioner interface {
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
	DeleteBucket(ctx context.Context, backendID, bucketName string) error
}

type Handler struct {
	repo        admindomain.BucketRepository
	provisioner Provisioner
	policy      cedar.Authorizer
}

func NewHandler(r admindomain.BucketRepository, p Provisioner, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("bucketh: policy authorizer is required")
	}
	return &Handler{repo: r, provisioner: p, policy: policyEngine}
}

// authorize evaluates Cedar against the Bucket resource. The Bucket entity
// is anchored under StorageBackend, so the engine sees both the backend
// and the bucket attributes.
func (h *Handler) authorize(ctx context.Context, action, backendID, bucketName string, ownerTenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
		action,
		&cedar.Resource{
			BackendID:     backendID,
			BucketName:    bucketName,
			OwnerTenantID: ownerTenantID,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

type CreateBucketInput struct {
	Bucket             admindomain.Bucket
	ProvisionOnBackend bool
}

func (h *Handler) CreateBucket(ctx context.Context, in CreateBucketInput) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if in.Bucket.BackendID == "" || in.Bucket.BucketName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id and bucket_name required"))
	}
	if err := h.authorize(ctx, actionManageBucket, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.OwnerTenantID); err != nil {
		return nil, err
	}
	if in.ProvisionOnBackend {
		if h.provisioner == nil {
			return nil, connect.NewError(connect.CodeUnavailable,
				errors.New("backend provisioning not wired"))
		}
		if err := h.provisioner.CreateBucket(ctx, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.Region); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if err := h.repo.Create(ctx, in.Bucket); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := h.repo.Get(ctx, in.Bucket.BackendID, in.Bucket.BucketName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetBucket(ctx context.Context, backendID, bucketName string) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, backendID, bucketName)
	if err != nil {
		return nil, mapNotFound(err)
	}
	// Cedar runs after the read so the engine sees authoritative ownership.
	if err := h.authorize(ctx, actionReadBucket, b.BackendID, b.BucketName, b.OwnerTenantID); err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && !apiutil.HasRole(ctx, apiutil.RoleBucketAdmin) {
		// Non-bucket-admins (i.e. tenant admins) get a redacted view: no
		// cedar_policy text, no replication target details. Avoids leaking
		// other tenants' policy graph.
		b.CedarPolicy = ""
		b.Replication.DestinationBucket = ""
	}
	return &b, nil
}

func (h *Handler) ListBuckets(ctx context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadBucket, args.BackendID, "", uuid.Nil); err != nil {
		return nil, "", err
	}
	return h.repo.List(ctx, args)
}

func (h *Handler) ListAccessibleBuckets(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]admindomain.Bucket, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return nil, "", connect.NewError(connect.CodePermissionDenied,
			errors.New("cannot enumerate accessible buckets for another tenant"))
	}
	if err := h.authorize(ctx, cedar.ActionReadBucket, "", "", tenantID); err != nil {
		return nil, "", err
	}
	return h.repo.ListAccessible(ctx, tenantID, pageSize, afterBackend, afterName)
}

// ─── Update ─────────────────────────────────────────────────────────────────

type UpdateBucketInput struct {
	Bucket          admindomain.Bucket
	ExpectedVersion int64
	UpdateMask      []string
}

func (h *Handler) UpdateBucket(ctx context.Context, in UpdateBucketInput) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBucket, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.OwnerTenantID); err != nil {
		return nil, err
	}
	if err := h.repo.UpdateBasic(ctx, in.Bucket, in.ExpectedVersion, in.UpdateMask); err != nil {
		return nil, mapVersion(err)
	}
	got, err := h.repo.Get(ctx, in.Bucket.BackendID, in.Bucket.BucketName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── Single-purpose setters ─────────────────────────────────────────────────

func (h *Handler) SetPolicy(ctx context.Context, backendID, bucketName, policy string, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureBucketPolicy, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetPolicy(ctx, backendID, bucketName, policy, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetLifecycleRules(ctx context.Context, backendID, bucketName string, rules []admindomain.LifecycleRule, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureLifecycle, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	// Validate every rule's CEL match upfront so a typo is rejected at
	// write time, not silently swallowed by the lifecycle worker hours
	// later. Empty match = "always match" (worker contract).
	for i, r := range rules {
		if err := celpkg.Validate(celpkg.ObjectSchema, r.Match); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("rule[%d] (id=%q): %w", i, r.ID, err))
		}
	}
	if err := h.repo.SetLifecycle(ctx, backendID, bucketName, rules, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetObjectLock(ctx context.Context, backendID, bucketName string, lock admindomain.ObjectLockConfig, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureLock, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetObjectLock(ctx, backendID, bucketName, lock, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetVersioning(ctx context.Context, backendID, bucketName string, v admindomain.BucketVersioning, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureVersioning, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetVersioning(ctx, backendID, bucketName, v, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetReplication(ctx context.Context, backendID, bucketName string, r admindomain.BucketReplication, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureReplication, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetReplication(ctx, backendID, bucketName, r, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

// ─── Delete ─────────────────────────────────────────────────────────────────

type DeleteBucketInput struct {
	BackendID       string
	BucketName      string
	ExpectedVersion int64
	DeleteOnBackend bool
}

func (h *Handler) DeleteBucket(ctx context.Context, in DeleteBucketInput) error {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return err
	}
	if err := h.authorize(ctx, actionManageBucket, in.BackendID, in.BucketName, uuid.Nil); err != nil {
		return err
	}
	if in.DeleteOnBackend {
		if h.provisioner == nil {
			return connect.NewError(connect.CodeUnavailable, errors.New("backend provisioning not wired"))
		}
		if err := h.provisioner.DeleteBucket(ctx, in.BackendID, in.BucketName); err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
	}
	if err := h.repo.Delete(ctx, in.BackendID, in.BucketName, in.ExpectedVersion); err != nil {
		return mapVersion(err)
	}
	return nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

func mapNotFound(err error) error {
	if errors.Is(err, admindomain.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func mapVersion(err error) error {
	if errors.Is(err, admindomain.ErrVersionMismatch) {
		return connect.NewError(connect.CodeAborted, err)
	}
	if errors.Is(err, admindomain.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

// silence unused
var _ = auth.AudienceAdmin
