package admindomain

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"
	"uuid"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

// ErrNotFound / ErrVersionMismatch are the canonical not-found / OCC errors
// for admin repositories. Handlers map these to connect.CodeNotFound and
// connect.CodeAborted respectively.
var (
	ErrNotFound        = errors.New("admin: resource not found")
	ErrVersionMismatch = errors.New("admin: resource_version mismatch")
	ErrConflict        = errors.New("admin: conflict")
	// ErrAlreadyExists — a create refused because the resource is already
	// there. Distinct from ErrConflict, which is FailedPrecondition and means
	// "some other precondition is unmet": a caller retrying a create needs to
	// tell "it exists" from "something else is wrong", and the code is the
	// only part of the answer they can branch on.
	ErrAlreadyExists = errors.New("admin: already exists")

	// ErrBucketOwnerRequired — a bucket named without the tenant that owns it
	// (ADR-0032).
	ErrBucketOwnerRequired = errors.New("admin: a bucket needs the tenant that owns it")
	// ErrBucketNotGranted — a tenant binding a collection to a bucket it
	// neither owns nor holds a grant on.
	ErrBucketNotGranted = errors.New("admin: the tenant neither owns the bucket nor holds a grant on it")
	// ErrBucketGrantInUse — a grant revoked while the tenant still has
	// collections in the bucket.
	ErrBucketGrantInUse = errors.New("admin: the tenant still has collections in the bucket")
)

// Register the admin domain sentinels with the central error→Connect-code
// mapper (ADR-0002) so admin handlers route through apiutil.MapError for a
// consistent code instead of a per-handler if/else.
func init() {
	apiutil.RegisterError(ErrNotFound, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND)
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT)
	apiutil.RegisterError(ErrConflict, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_CONFLICT)
	apiutil.RegisterError(ErrAlreadyExists, connect.CodeAlreadyExists, commonv1.ErrorReason_ERROR_REASON_ALREADY_EXISTS)
	apiutil.RegisterError(ErrBucketOwnerRequired, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_BUCKET_OWNER_REQUIRED)
	apiutil.RegisterError(ErrBucketNotGranted, connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_GRANTED)
	apiutil.RegisterError(ErrBucketGrantInUse, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_BUCKET_GRANT_IN_USE)
}

// ─── Storage backend repository ─────────────────────────────────────────────

type BackendRepository interface {
	// Create inserts a NEW backend; a name in use returns ErrAlreadyExists.
	// Upsert is the seeding path's convergence write and must not be used to
	// serve the Create RPC — see the query comments.
	Create(ctx context.Context, b StorageBackend) error
	Upsert(ctx context.Context, b StorageBackend) error
	Get(ctx context.Context, backendID string) (StorageBackend, error)
	// filter is the caller's CEL expression. The repo pushes its
	// SQL-expressible conjuncts into the query; the handler still evaluates
	// the whole expression over the returned page, so pushdown may only
	// narrow the candidate set, never decide the answer.
	List(ctx context.Context, pageSize int32, afterID, filter string) ([]StorageBackend, string, error)
	Update(ctx context.Context, b StorageBackend, expectedVersion int64, mask []string) error
	SetEnabled(ctx context.Context, backendID string, enabled bool, expectedVersion int64) error
	// SetReadOnly flips the drain (read-only) state (the schema baseline (001_initial_schema.sql)). Same
	// OCC + operator-managed contract as SetEnabled.
	SetReadOnly(ctx context.Context, backendID string, readOnly bool, expectedVersion int64) error
	// SetMaintenance flips the operator-set maintenance flag (the schema baseline (001_initial_schema.sql)).
	// Same OCC + operator-managed contract as SetReadOnly; advisory only.
	SetMaintenance(ctx context.Context, backendID string, maintenance bool, expectedVersion int64) error
	// SetHealth records the outcome of a TestBackend probe (the schema baseline (001_initial_schema.sql)).
	// DERIVED/advisory: no OCC, writes a separate 1:1 table so it never
	// bumps the backend's resource_version. status is "ok" | "error".
	SetHealth(ctx context.Context, backendID, status, message string, checkedAt time.Time) error
	// SetFeatures replaces the recorded feature probe results (ADR-0026).
	// Advisory like SetHealth: no OCC, no resource_version change.
	SetFeatures(ctx context.Context, backendID string, results []features.Result) error
	// RotateCredentials swaps credentials_secret_ref to secretRef. When
	// graceSeconds > 0 it preserves the prior ref in
	// previous_credentials_secret_ref with a now()+grace validity horizon so
	// in-flight presigns signed against the old credentials are observably
	// still valid; 0 rotates instantly (clears the previous window).
	RotateCredentials(ctx context.Context, backendID, secretRef string, graceSeconds int64) error
	Delete(ctx context.Context, backendID string, expectedVersion int64) error
}

// ─── Bucket repository ──────────────────────────────────────────────────────

// BucketReference is one relation that can hold a bucket back, and how many
// of its rows currently do.
type BucketReference struct {
	Relation string
	Count    int64
}

// BucketGrant is a tenant other than its owner that may bind collections to a
// bucket (ADR-0032).
type BucketGrant struct {
	TenantID  uuid.UUID
	GrantedBy string
	GrantedAt time.Time
}

// BucketGrantRepository keeps a bucket's grants. A bucket that does not exist
// is ErrNotFound.
type BucketGrantRepository interface {
	// Grant is idempotent: a tenant holding a grant keeps it as it was.
	Grant(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID, grantedBy string) (BucketGrant, error)
	// RevokeGrant refuses with ErrBucketGrantInUse while the tenant has
	// collections in the bucket; a grant that is not there is ErrNotFound.
	RevokeGrant(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) error
	ListGrants(ctx context.Context, backendID, bucketName string) ([]BucketGrant, error)
	// MayBind reports whether the tenant owns the bucket or holds a grant.
	MayBind(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) (bool, error)
}

type BucketRepository interface {
	Create(ctx context.Context, b Bucket) error
	Get(ctx context.Context, backendID, bucketName string) (Bucket, error)
	List(ctx context.Context, args ListBucketsArgs) ([]Bucket, string, error)
	ListAccessible(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]Bucket, string, error)
	UpdateBasic(ctx context.Context, b Bucket, expectedVersion int64, mask []string) error
	SetPolicy(ctx context.Context, backendID, bucketName, policy string, expectedVersion int64) error
	SetLifecycle(ctx context.Context, backendID, bucketName string, rules []LifecycleRule, expectedVersion int64) error
	SetObjectLock(ctx context.Context, backendID, bucketName string, lock ObjectLockConfig, expectedVersion int64) error
	SetVersioning(ctx context.Context, backendID, bucketName string, v BucketVersioning, expectedVersion int64) error
	SetReplication(ctx context.Context, backendID, bucketName string, r BucketReplication, expectedVersion int64) error
	SetConstraints(ctx context.Context, backendID, bucketName string, c BucketConstraints, expectedVersion int64) error
	Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error
	// CountBucketReferences reports, per relation, how many rows hold this
	// bucket under ON DELETE RESTRICT — every relation that can refuse the
	// delete. Relations with a zero count are included; the caller filters.
	//
	// Buckets are platform-level and almost every relation here is
	// tenant-scoped, so the caller must run this under a cross-tenant read:
	// a tenant-scoped session sees zero rows and concludes the bucket is
	// free, while the foreign-key check — which does not consult RLS —
	// refuses the delete anyway.
	CountBucketReferences(ctx context.Context, backendID, bucketName string) ([]BucketReference, error)
	// BackendEnabled reports whether the named storage backend is enabled.
	// Returns ErrNotFound when the backend id is unknown. Used by
	// CreateBucket to refuse binding a bucket to a disabled backend
	// (feature 002) — the object-path gate covers everything else.
	BackendEnabled(ctx context.Context, backendID string) (bool, error)

	// ─── outbox / reconciler ────────────────────────────────────────────
	// ListPendingProvisions returns up to `limit` rows that need the
	// backend's CreateBucket call. Includes 'pending' rows and 'failed'
	// rows that have not yet exhausted their retry budget (`maxAttempts`).
	ListPendingProvisions(ctx context.Context, maxAttempts, limit int32) ([]BucketProvisionRow, error)
	MarkProvisionReady(ctx context.Context, backendID, bucketName string) error
	// MarkProvisionFailed records an attempt error. terminal=true marks
	// the row 'failed' and stops the worker from retrying; terminal=false
	// (transient) keeps it 'pending' for the next tick.
	MarkProvisionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error

	// ─── outbox / delete path ───────────────────────────────────────────
	// MarkDeleting flips a row into the deletion outbox. The handler calls
	// this; the actual physical delete (both S3 + DB row) is the worker's
	// job. expectedVersion=0 disables the OCC check.
	MarkDeleting(ctx context.Context, backendID, bucketName string, expectedVersion int64) error
	// ListPendingDeletions is the deletion-side companion of ListPending
	// Provisions: 'deleting' rows + 'deletion_failed' rows still within
	// the retry budget.
	ListPendingDeletions(ctx context.Context, maxAttempts, limit int32) ([]BucketProvisionRow, error)
	// MarkDeletionFailed records a failed delete attempt. terminal=true
	// freezes the row in 'deletion_failed'; terminal=false keeps it
	// 'deleting' for retry.
	MarkDeletionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error
}

type ListBucketsArgs struct {
	BackendID string // "" → all
	// OwnerTenantID — when non-nil, narrows the listing to buckets
	// owned by exactly that tenant (matches buckets.owner_tenant_id).
	// nil = no tenant filter (cross-tenant listing for platform-admin).
	OwnerTenantID *uuid.UUID
	// UsedByTenantID — when non-nil, narrows the listing to the buckets
	// that tenant stores in: owned, bound by default, or holding one of
	// its collections. A shared-layout tenant owns none, so this is the
	// question a tenant's own bucket list asks. nil = no such filter.
	UsedByTenantID *uuid.UUID
	PageSize       int32
	AfterBackend   string
	AfterName      string
	// Filter is a CEL expression over PhysicalBucketSchema. The repo pushes
	// its SQL-expressible conjuncts into the query and the handler evaluates
	// the whole expression over the page it gets back; the repo cursor is
	// returned unchanged, so a page whose rows all fail the predicate is a
	// legitimate empty page with a next token.
	Filter string
}

// ─── Audit repository ───────────────────────────────────────────────────────

type AuditRepository interface {
	Insert(ctx context.Context, e AuditEntry) error
	// InsertWithOutbox inserts the entry and, when onInserted is non-nil,
	// runs it inside the SAME transaction before commit — so the audit row
	// and any fan-out outbox rows commit atomically (ADR-0003 transactional
	// outbox). onInserted == nil behaves exactly like Insert.
	InsertWithOutbox(ctx context.Context, e AuditEntry, onInserted func(ctx context.Context, tx pgx.Tx) error) error
	Get(ctx context.Context, entryID uuid.UUID) (AuditEntry, error)
	List(ctx context.Context, args ListAuditArgs) ([]AuditEntry, string, error)
	// CountFacets counts the entries List would list for args, without its
	// order, cursor or page size, by each facet's answers: each facet
	// counted without its own choices in args.Facets.
	CountFacets(ctx context.Context, args ListAuditArgs) ([]AuditFacetCount, error)
}

// The facets an audit listing narrows and counts by (AuditFacetCount.Facet).
const (
	AuditFacetPlane   = "plane"
	AuditFacetOutcome = "outcome"
	AuditFacetService = "service"
)

// AuditOutcome is how a recorded call ended: an AuditFacetOutcome answer.
type AuditOutcome string

const (
	AuditOutcomeOK    AuditOutcome = "ok"
	AuditOutcomeError AuditOutcome = "error"
)

// AuditFacets narrows an audit listing: an entry passes when its plane
// (ActorAudience) is one of Planes, its outcome one of Outcomes and its
// service (the package and service of its action's procedure, or the action
// itself for one of the server's own) one of Services, each empty for any.
type AuditFacets struct {
	Planes   []string
	Outcomes []AuditOutcome
	Services []string
}

// AuditFacetCount is how many entries of a listing give Value for Facet.
type AuditFacetCount struct {
	Facet, Value string
	Count        int64
}

// The fields ListAuditLog sorts by, as order_by names them: AuditLogEntry's
// own field names. list_audit_entry_ids (migrations 086, 093) maps each to its
// column, and an integration test sorts by every one.
const (
	AuditOrderByAt       = "at"
	AuditOrderByAction   = "action"
	AuditOrderByActor    = "actor_subject"
	AuditOrderByResource = "resource_name"
	// AuditOrderByClient sorts by the User-Agent; a call that sent none
	// sorts as empty.
	AuditOrderByClient = "user_agent"
)

// AuditOrderFields lists every order_by ListAuditLog accepts.
var AuditOrderFields = []string{AuditOrderByAt, AuditOrderByAction, AuditOrderByActor, AuditOrderByResource, AuditOrderByClient}

// DefaultAuditOrder is the order of an empty order_by: newest first.
var DefaultAuditOrder = keyset.Order{Field: AuditOrderByAt, Desc: true}

// AuditSortValue is e's value of an order field, as a page token carries it.
func AuditSortValue(field string, e AuditEntry) string {
	switch field {
	case AuditOrderByAction:
		return e.Action
	case AuditOrderByActor:
		return e.ActorSubject
	case AuditOrderByResource:
		return e.ResourceName
	case AuditOrderByClient:
		return e.UserAgent
	default:
		return e.At.UTC().Format(time.RFC3339Nano)
	}
}

// CompareAuditEntries orders a and b the way list_audit_entry_ids does: by
// the field — text bytewise, as COLLATE "C" does — then by id, the whole
// reversed for a descending order.
func CompareAuditEntries(order keyset.Order, a, b AuditEntry) int {
	var c int
	if order.Field == AuditOrderByAt {
		c = a.At.Compare(b.At)
	} else {
		c = strings.Compare(AuditSortValue(order.Field, a), AuditSortValue(order.Field, b))
	}
	if c == 0 {
		c = bytes.Compare(a.EntryID[:], b.EntryID[:])
	}
	if order.Desc {
		return -c
	}
	return c
}

// AuditCursor is the page token that resumes a listing in order after e.
// ListAuditLog's handler and the store both cut pages, and both mint it here.
func AuditCursor(order keyset.Order, e AuditEntry) string {
	return keyset.Encode(keyset.Cursor{Order: order, Value: AuditSortValue(order.Field, e), ID: e.EntryID})
}

type ListAuditArgs struct {
	ActorSubject  string
	ActorTenantID uuid.UUID
	// TrailTenantID selects a tenant's trail: entries its principals made OR
	// entries on its resources (AuditEntry.ResourceTenant). uuid.Nil = all.
	TrailTenantID uuid.UUID
	// Order is the sort; the handler resolves an empty one to
	// DefaultAuditOrder. PageToken, when set, was minted for it.
	Order     keyset.Order
	PageToken string
	PageSize  int32

	// CEL-pushdown predicates. Optional — populated by the audit
	// handler from the caller's CEL filter when a recognised conjunct
	// can be expressed in SQL. The handler still runs the full CEL
	// program in-memory afterwards (correctness invariant), so these
	// fields only narrow the candidate set, never replace evaluation.
	//
	// Empty string / zero-value means "no SQL predicate"; a populated
	// field becomes a top-level AND.
	ActionEq     string    // exact match on action
	ActionPrefix string    // LIKE '<prefix>%'
	AtGTE        time.Time // at >= AtGTE
	AtLTE        time.Time // at <= AtLTE

	// Facets narrows the listing apart from the filter.
	Facets AuditFacets
}

// ─── Quota repository ───────────────────────────────────────────────────────

type QuotaRepository interface {
	UpsertTenant(ctx context.Context, q Quota) error
	UpsertBucket(ctx context.Context, q Quota) error
	GetTenant(ctx context.Context, tenantID uuid.UUID) (Quota, error)
	GetBucket(ctx context.Context, backendID, bucketName string) (Quota, error)
	IncrementUsage(ctx context.Context, quotaID uuid.UUID, deltaBytes, deltaCount int64) error
	ResetDaily(ctx context.Context, quotaID uuid.UUID, at time.Time) error
}

// ─── Event subscription repository ──────────────────────────────────────────

type EventSubscriptionRepository interface {
	// Create persists s and stamps the generated SubscriptionID back
	// onto the input. Pointer receiver is load-bearing — the handler
	// uses s.SubscriptionID immediately after to fetch the freshly
	// inserted row, and a value-receiver would silently retain the
	// caller's zero UUID, causing the post-Create Get to look up
	// `subscription_id = '00000000-...'` and return ErrNotFound.
	Create(ctx context.Context, s *EventSubscription) error
	Get(ctx context.Context, id uuid.UUID) (EventSubscription, error)
	List(ctx context.Context, args ListEventSubscriptionsArgs) ([]EventSubscription, string, error)
	Update(ctx context.Context, s EventSubscription, expectedVersion int64, mask []string) error
	Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error
	// RequeueFailedDeliveries queues the subscription's failed deliveries
	// again and returns how many it queued.
	RequeueFailedDeliveries(ctx context.Context, id uuid.UUID) (int64, error)
}

type ListEventSubscriptionsArgs struct {
	TenantID uuid.UUID
	PageSize int32
	AfterID  uuid.UUID
}
