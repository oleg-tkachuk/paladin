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
	// Outbox model: the DB row is the source of truth. When the caller
	// asked us to create the physical bucket too, we mark the row
	// 'pending' and let the reconciler worker drive the backend
	// CreateBucket — that way a DB-write failure can never leave an
	// orphan in S3, and a backend-side failure is observable on the row
	// instead of being lost to a 5xx that never made it to the client.
	if in.ProvisionOnBackend {
		if h.provisioner == nil {
			return nil, connect.NewError(connect.CodeUnavailable,
				errors.New("backend provisioning not wired"))
		}
		in.Bucket.ProvisionState = admindomain.BucketProvisionStatePending
	} else {
		// Operator opted out of backend provisioning (e.g. binding a
		// pre-existing bucket). Row is immediately authoritative.
		in.Bucket.ProvisionState = admindomain.BucketProvisionStateReady
	}
	if err := h.repo.Create(ctx, in.Bucket); err != nil {
		// Translate the typed ErrConflict the repo raises for FK /
		// unique violations into FailedPrecondition so clients (UI,
		// SDKs) see a readable message instead of "internal: SQLSTATE
		// 23503". The repo's wrapped error already names the missing
		// backend or duplicate bucket.
		if errors.Is(err, admindomain.ErrConflict) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
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
	// Outbox model for deletes (mirrors CreateBucket): the row stays in
	// place flipped to 'deleting', and the bucket-reconciler worker
	// drives the physical s3.DeleteBucket then the actual row removal.
	// That avoids the same orphan window the create path used to have.
	//
	// When the operator opted out of backend deletion, we still mark
	// 'deleting' rather than physical-delete inline — the worker has
	// the same code path that handles the "no provisioner" case (it
	// just deletes the row without calling S3) and concentrating the
	// terminal logic there keeps the handler simple.
	if in.DeleteOnBackend && h.provisioner == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("backend provisioning not wired"))
	}
	if !in.DeleteOnBackend {
		// Operator says S3 cleanup is their problem — physically delete
		// the row right away. Faster and avoids parking dev/test rows
		// in a 'deleting' loop the worker can never resolve (no S3 →
		// always transient).
		if err := h.repo.Delete(ctx, in.BackendID, in.BucketName, in.ExpectedVersion); err != nil {
			return mapVersion(err)
		}
		return nil
	}
	if err := h.repo.MarkDeleting(ctx, in.BackendID, in.BucketName, in.ExpectedVersion); err != nil {
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
