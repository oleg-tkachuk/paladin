// Package object implements the ObjectService Connect handler.
//
// This is the exemplar handler that wires together:
//
//   - auth (tenant from ctx)
//   - Cedar (authorization decision)
//   - CEL (filter evaluation for ListObjects)
//   - Storage (presign / HEAD / copy)
//   - State machine (idempotent promotion / delete / restore)
//
// Other services (ObjectKey, Presign, Multipart, Batch, Tenant, Operation)
// follow the same shape.
package object

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/capability"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// EventProducer mirrors the seam used by the admin handlers — narrow
// interface, *worker.Dispatcher implements it.
//
// Object lifecycle is the highest-cardinality producer in the
// system: every CompleteObject call fans out, so the per-tenant
// CEL filters on EventSubscriptions become load-bearing for any
// realistic object workload. A subscription with an empty filter
// catches every event class; subscribers that only care about
// `paladin.object.uploaded` should set
//
//	`event.kind == 'paladin.object.uploaded'`
//
// (see frontend/src/app/events/page.tsx hint copy) so the
// dispatcher's per-row List doesn't queue rows the consumer would
// drop anyway.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
	// DispatchTx writes the outbox rows on the caller's transaction so the
	// fan-out is atomic with the state change (ADR-0003). Used by the
	// promote path; the other lifecycle events still use best-effort
	// Dispatch until they adopt the same tx flow.
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

// Storage abstracts S3 / GCS / MinIO. Keep this interface intentionally
// narrow — handler logic does not know which backend it's talking to.
type Storage interface {
	PresignPut(ctx context.Context, args PresignPutArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPost(ctx context.Context, args PresignPostArgs) (action string, fields map[string]string, expiresAt time.Time, err error)
	PresignGet(ctx context.Context, args PresignGetArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	Head(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
	CopyObject(ctx context.Context, src, dst Location) error
	// DeleteObject is optional — for permanent deletes only.
	DeleteObject(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key string) error
	// CompletionMode is derived from the objectKey's storage backend config.
	CompletionMode(objectKey string) CompletionMode
}

// BucketMeta is the minimal projection of bucket metadata the object
// handler needs at promote / delete time. Returned by LookupBucketMeta.
type BucketMeta struct {
	BackendID         string
	BucketName        string
	VersioningEnabled bool
	ObjectLockEnabled bool
	// EventsEnabled mirrors storage_backends.events_enabled; drives the
	// upload CompletionMode (implicit via bucket events vs explicit
	// CompleteUpload call) without a second lookup on the upload path.
	EventsEnabled bool
}

// ObjectLock is the object row's lock state (object-level, distinct from
// the per-version locks the object_versions trigger enforces).
type ObjectLock struct {
	Mode        string // "", "GOVERNANCE", or "COMPLIANCE"
	RetainUntil *time.Time
	LegalHold   bool
}

// Active reports whether the lock blocks deletion right now. governanceBypass
// only relaxes a GOVERNANCE retention window; legal hold and COMPLIANCE are
// absolute. Mirrors enforce_object_version_lock() and the HardDelete SQL guard.
func (l ObjectLock) Active(now time.Time, governanceBypass bool) bool {
	if l.LegalHold {
		return true
	}
	retained := l.RetainUntil != nil && l.RetainUntil.After(now)
	if l.Mode == "COMPLIANCE" && retained {
		return true
	}
	if l.Mode == "GOVERNANCE" && retained && !governanceBypass {
		return true
	}
	return false
}

// Reason renders a human error fragment for an active lock.
func (l ObjectLock) Reason() string {
	if l.LegalHold {
		return "object is under legal hold"
	}
	if l.RetainUntil != nil {
		return fmt.Sprintf("%s retention lock active until %s", l.Mode, l.RetainUntil.Format(time.RFC3339))
	}
	return "object is locked"
}

// ErrBackendDisabled is returned by the bucket-resolution path
// (LookupBucket / LookupBucketMeta) when the resolved storage backend
// has been disabled (feature 002). It is the single chokepoint that
// guarantees a disabled backend processes NO PALADIN-mediated operation:
// every object/presign/multipart/copy path resolves its bucket through
// the resolver first, so none can reach the object store. Handlers map
// it to CodeFailedPrecondition via mapResolveErr.
//
// NOTE: this cannot revoke presigned URLs already issued — those hit the
// object store directly, bypassing PALADIN, and expire on their own TTL.
// Disabling only blocks issuance of NEW presigns and PALADIN-mediated ops.
var ErrBackendDisabled = errors.New("storage backend is disabled")

// mapResolveErr maps a bucket-resolution error to the right Connect code:
// a disabled backend is FailedPrecondition (the resource exists but is
// not in a state that permits the op); anything else is treated as
// NotFound (the historical behaviour for an unresolved object key).
func MapResolveErr(err error) error {
	if errors.Is(err, ErrBackendDisabled) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeNotFound, err)
}

type CompletionMode uint8

const (
	CompletionModeUnspecified CompletionMode = iota
	CompletionModeImplicit
	CompletionModeExplicit
)

// Location identifies an S3 object: the physical bucket plus the
// composed key (tenant_id/object_key/key). Bucket may be empty, in
// which case the storage adapter falls back to its configured default.
type Location struct {
	TenantID  uuid.UUID
	Bucket    string // physical S3 bucket
	ObjectKey string // PALADIN namespace within the bucket
	Key       string // storage key inside the prefix
}

type PresignPutArgs struct {
	TenantID        uuid.UUID
	Bucket          string // physical S3 bucket; resolved from ObjectKey row
	ObjectKey       string
	Key             string
	ContentType     string
	ChecksumAlgo    string
	SizeHint        int64
	TTL             time.Duration
	RequireChecksum bool
}

type PresignPostArgs struct {
	TenantID     uuid.UUID
	Bucket       string
	ObjectKey    string
	Key          string
	ContentType  string
	MaxSizeBytes int64
	ChecksumAlgo string
	TTL          time.Duration
}

type PresignGetArgs struct {
	TenantID           uuid.UUID
	Bucket             string
	ObjectKey          string
	Key                string
	TTL                time.Duration
	ContentDisposition string
}

// Repository is the persistence seam. Implementations live in internal/store
// (sqlc-backed). Keeping it local to this package keeps handler tests lean.
type Repository interface {
	CreateObject(ctx context.Context, args CreateObjectArgs) (Object, error)
	FindByName(ctx context.Context, tenantID uuid.UUID, objectKey, objectID string) (Object, error)
	// FindByIDs returns the rows for the given ids in a single query.
	// Missing ids are simply absent from the result — callers diff
	// against their input to report per-id not-found. Exists so batch
	// executors don't issue one FindByName round-trip per id.
	FindByIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]Object, error)
	FindByPath(ctx context.Context, tenantID uuid.UUID, objectKey, key string) (Object, error)
	UpdateMetadata(ctx context.Context, args UpdateMetadataArgs) (Object, error)
	// UpdateMetadataTx runs UpdateMetadata on tx so the handler can write
	// the paladin.object.updated outbox rows atomically with the row update
	// (ADR-0003). RunInTx supplies the tx.
	UpdateMetadataTx(ctx context.Context, tx pgx.Tx, args UpdateMetadataArgs) (Object, error)
	ListObjects(ctx context.Context, args ListObjectsArgs) ([]Object, string, error)
	CountObjects(ctx context.Context, args CountObjectsArgs) (count int64, exact bool, err error)
	// ListDistinctTags returns the distinct tag key→values across the
	// ObjectKey's live (non-DELETED) objects, each value list sorted. Backs
	// the tag-facet filter dropdown.
	ListDistinctTags(ctx context.Context, tenantID uuid.UUID, objectKey string) (map[string][]string, error)
	// LookupBucket returns the physical S3 bucket for a tenant's ObjectKey.
	// Cheap lookup (covered by idx_object_keys_bucket_routing). Empty
	// string means the row exists but no bucket has been bound — the
	// storage adapter falls back to its configured default in that case.
	LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, error)
	// LookupBucketMeta returns the bucket binding plus the metadata needed for
	// versioning / lock decisions on the hot path. Implementations should
	// satisfy this with a single query — handlers call it on every promote.
	LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, objectKey string) (BucketMeta, error)
	// ObjectLock returns the object row's lock state so the delete path
	// can refuse (and report) a locked object before touching storage.
	ObjectLock(ctx context.Context, tenantID, objectID uuid.UUID) (ObjectLock, error)
	// HardDelete removes the row outright; caller is responsible for
	// having already deleted the storage-side object. expectedVersion=0
	// skips OCC. Returns ErrVersionMismatch if no rows affected. The SQL
	// also refuses locked rows (legal hold / active COMPLIANCE; active
	// GOVERNANCE unless the bypass variant set the session GUC).
	HardDelete(ctx context.Context, tenantID, objectID uuid.UUID, expectedVersion int64) error
	// HardDeleteWithBypass performs the same delete as HardDelete but inside
	// a transaction that sets `SET LOCAL paladin.governance_bypass = true`, which
	// the object_versions trigger reads to permit removal of GOVERNANCE-locked
	// rows. COMPLIANCE-locked rows are still rejected by the trigger.
	HardDeleteWithBypass(ctx context.Context, tenantID, objectID uuid.UUID, expectedVersion int64) error
	// HardDeleteTx / HardDeleteWithBypassTx run the permanent delete on the
	// caller's tx so the handler can enqueue paladin.object.deleted atomically
	// with the row removal (ADR-0003). The bypass variant sets the governance
	// GUC on that same tx. RunInTx supplies the tx.
	HardDeleteTx(ctx context.Context, tx pgx.Tx, tenantID, objectID uuid.UUID, expectedVersion int64) error
	HardDeleteWithBypassTx(ctx context.Context, tx pgx.Tx, tenantID, objectID uuid.UUID, expectedVersion int64) error
	// RunInTx runs fn in one transaction on the repo's pool — the seam the
	// update / permanent-delete handlers use to write the mutation and its
	// outbox rows atomically.
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	// LiveCollision reports whether a non-DELETED row already occupies
	// (tenant, object_key, key); used to refuse RestoreObject when the
	// slot has been reused by a fresh upload.
	LiveCollision(ctx context.Context, tenantID uuid.UUID, objectKey, key string) (bool, error)
}

type Object struct {
	ObjectID         uuid.UUID
	TenantID         uuid.UUID
	BackendID        string // FK column from object_keys; populated when JOINed
	Bucket           string // physical S3 bucket; populated when JOINed
	ObjectKey        string
	Key              string
	State            statemachine.State
	ContentType      string
	SizeBytes        int64
	ETag             string
	ChecksumAlgo     string // CRC32C / SHA256 / MD5 — propagated from CreateObjectArgs
	Checksum         string
	Sequencer        string
	Metadata         map[string]string
	Tags             map[string]string
	ExternalRef      string
	ResourceVersion  int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CommittedAt      *time.Time
	TerminatedAt     *time.Time
	PresignExpiresAt *time.Time
}

type CreateObjectArgs struct {
	TenantID         uuid.UUID
	ObjectKey        string
	Key              string
	ContentType      string
	SizeHint         int64
	ChecksumAlgo     string
	Metadata         map[string]string
	Tags             map[string]string
	ExternalRef      string
	PresignExpiresAt time.Time
}

type UpdateMetadataArgs struct {
	TenantID        uuid.UUID
	ObjectID        uuid.UUID
	ResourceVersion int64
	UpdatedFields   []string // "metadata","tags","content_type","external_ref"
	Metadata        map[string]string
	Tags            map[string]string
	ContentType     string
	ExternalRef     string
}

type ListObjectsArgs struct {
	TenantID    uuid.UUID
	ObjectKey   string
	PageSize    int32
	PageToken   string
	CompiledCEL cel.Program // pre-compiled; nil = no filter
	// Filter is the raw CEL expression, kept alongside CompiledCEL so the
	// adapter can extract SQL pushdown predicates (state/key) from it. The
	// CompiledCEL remains authoritative as a post-load pass.
	Filter   string
	OrderBy  string
	SortDesc bool
}

// CountObjectsArgs carries the inputs for Repository.CountObjects. When
// CompiledCEL is nil the adapter can short-circuit to a COUNT(*) query and
// return exact=true; otherwise it iterates the table applying the filter and
// may return an approximate (capped) count.
type CountObjectsArgs struct {
	TenantID    uuid.UUID
	ObjectKey   string
	CompiledCEL cel.Program
}

// Handler is the Connect service implementation.
type Handler struct {
	repo    Repository
	storage Storage
	policy  cedar.Authorizer
	filter  *cel.Evaluator
	sm      *statemachine.Transitioner
	presign PresignConfig
	// versions is the optional version recorder. When set + the parent
	// bucket has versioning enabled, the handler emits version history rows
	// on promote / soft-delete. Nil disables versioning side-effects entirely.
	versions *VersionHandler

	// quota is the optional accounting hook called after a successful
	// promote. Increments tenant-scope usage counters; the post-completion
	// hard check that complements the presign-time soft check lives there.
	quota QuotaUpdater

	events EventProducer
	log    *zap.Logger
}

// SetVersionHandler attaches the optional version recorder. Wired by main.
func (h *Handler) SetVersionHandler(v *VersionHandler) { h.versions = v }

// QuotaUpdater is the post-promote accounting hook. Returns nil on missing
// quota — quotas are opt-in. Implementations live in postgres adapters.
type QuotaUpdater interface {
	OnObjectPromoted(ctx context.Context, tenantID uuid.UUID, sizeBytes int64) error
}

// SetQuotaUpdater attaches the optional usage hook. Wired by main.
func (h *Handler) SetQuotaUpdater(q QuotaUpdater) { h.quota = q }

// SetEventProducer / SetLogger — same opt-in contract as the admin
// handlers. nil-safe via dispatchEvent's guard.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEventTx fans the lifecycle event out on the caller's tx so the
// outbox rows commit atomically with the state transition (ADR-0003). An
// error propagates to the caller, which rolls the transition back — the
// client's at-least-once retry re-runs both. Used by the transactional
// promote / soft-delete / restore paths.
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil {
		return nil
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	_, err := h.events.DispatchTx(ctx, tx, tenantID.String(), worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: resourceName,
		ActorSubject: actor,
		Payload:      payload,
	})
	return err
}

// objectResourceName is the C-shape (tenant-first) object resource name. Kept
// as the fallback the canonical builders degrade to when the (backend, bucket)
// binding can't be resolved.
func objectResourceName(tenantID uuid.UUID, objectKey, key string) string {
	return fmt.Sprintf("tenants/%s/objectKeys/%s/objects-by-key/%s", tenantID, objectKey, key)
}

// canonicalObjectPrefix resolves the canonical (A-shape) objectKey prefix
// `storageBackends/{b}/buckets/{bk}/tenants/{tid}/objectKeys/{ok}` used to build
// object-level event resource names (ADR-0010 Phase 1). The (backend, bucket)
// binding depends only on the objectKey, so callers resolve it ONCE before the
// mutation tx and pass it into the dispatch — never a pool read inside an open
// tx. On a lookup miss it returns "" and the caller falls back to the C-shape
// name; a transient resolve blip must never block the event.
func (h *Handler) canonicalObjectPrefix(ctx context.Context, tenantID uuid.UUID, objectKey string) string {
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, objectKey)
	if err != nil || meta.BackendID == "" || meta.BucketName == "" {
		return ""
	}
	return fmt.Sprintf("storageBackends/%s/buckets/%s/tenants/%s/objectKeys/%s",
		meta.BackendID, meta.BucketName, tenantID, objectKey)
}

// objectResourceNameFrom builds the object event resource name: canonical (A)
// when the pre-resolved prefix is non-empty, else the C-shape fallback. The
// `/objects-by-key/` anchor + user key are appended verbatim (the user key may
// contain '/').
func objectResourceNameFrom(canonicalPrefix string, tenantID uuid.UUID, objectKey, key string) string {
	if canonicalPrefix == "" {
		return objectResourceName(tenantID, objectKey, key)
	}
	return canonicalPrefix + "/objects-by-key/" + key
}

type PresignConfig struct {
	DefaultTTL     time.Duration
	MaxTTL         time.Duration
	DefaultMaxSize int64
}

func NewHandler(
	repo Repository,
	storage Storage,
	policy cedar.Authorizer,
	filter *cel.Evaluator,
	sm *statemachine.Transitioner,
	cfg PresignConfig,
) *Handler {
	return &Handler{
		repo:    repo,
		storage: storage,
		policy:  policy,
		filter:  filter,
		sm:      sm,
		presign: cfg,
	}
}

// ─── Exemplar RPC: UploadObject ─────────────────────────────────────────────
//
// Full flow shown here; other RPCs follow the same shape.

// UploadObjectInput is the decoded request. In production wiring, this comes
// from the generated Connect stub (paladinv1.UploadObjectRequest).
type UploadObjectInput struct {
	ObjectKey     string
	Key           string
	ContentType   string
	SizeHint      int64
	ChecksumAlgo  string
	Metadata      map[string]string
	Tags          map[string]string
	ExternalRef   string
	TransportPOST bool
}

type UploadObjectOutput struct {
	Object         Object
	URL            string
	Method         string
	Headers        map[string]string
	ExpiresAt      time.Time
	CompletionMode CompletionMode
	// Populated when TransportPOST is true.
	PostAction string
	PostFields map[string]string
}

// UploadObject is the fully-wired business logic. The Connect adapter is
// a thin shim that decodes protobuf into UploadObjectInput, calls this,
// and encodes UploadObjectOutput back.
func (h *Handler) UploadObject(ctx context.Context, in UploadObjectInput) (*UploadObjectOutput, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	objectURI := "object://" + tenantID.String() + "/" + in.ObjectKey + "/" + in.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}

	// 1. Cedar authorization: may this principal PutObject here?
	principal, _ := auth.PrincipalFromContext(ctx)
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: principal.Subject, TenantID: tenantID, TenantSlug: principal.TenantSlug, Roles: principal.Roles, Scopes: apiutil.ScopeStrings(principal.Scopes)},
		cedar.ActionPresignPut,
		&cedar.Resource{
			TenantID:    tenantID,
			ObjectKey:   in.ObjectKey,
			Key:         in.Key,
			ContentType: in.ContentType,
			SizeBytes:   in.SizeHint,
			Tags:        in.Tags,
		},
		cedar.RequestContext{
			SizeBytes:   in.SizeHint,
			ContentType: in.ContentType,
			Now:         time.Now(),
		},
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}

	// 2. Resolve bucket binding + completion mode in ONE lookup —
	//    LookupBucketMeta joins the same object_keys × storage_backends
	//    rows the old BucketCompletionMode + LookupBucket pair each
	//    queried separately, and keeps the disabled-backend chokepoint.
	//    Empty BucketName means "row exists but bucket_name is NULL" —
	//    the storage adapter falls back to its configured default; after
	//    migration 005 / startup backfill this case is impossible.
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, in.ObjectKey)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	completion := CompletionModeExplicit
	if meta.EventsEnabled {
		completion = CompletionModeImplicit
	}
	bucket := meta.BucketName

	// 3. Generate UUIDv7 for object_id; default key = <object_id> under tenant prefix.
	objectID := uuid.Must(uuid.NewV7())
	key := in.Key
	if key == "" {
		key = objectID.String()
	}

	// 4. Insert PENDING row with presign expiry.
	ttl := h.presign.DefaultTTL
	presignExp := time.Now().Add(ttl)
	obj, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID:         tenantID,
		ObjectKey:        in.ObjectKey,
		Key:              key,
		ContentType:      in.ContentType,
		SizeHint:         in.SizeHint,
		ChecksumAlgo:     in.ChecksumAlgo,
		Metadata:         in.Metadata,
		Tags:             in.Tags,
		ExternalRef:      in.ExternalRef,
		PresignExpiresAt: presignExp,
	})
	if err != nil {
		return nil, mapCreateErr(err)
	}

	// 5. Generate presign. PUT or POST depending on client preference.
	out := &UploadObjectOutput{
		Object:         obj,
		CompletionMode: completion,
		Method:         "PUT",
	}
	if in.TransportPOST {
		action, fields, exp, err := h.storage.PresignPost(ctx, PresignPostArgs{
			TenantID:     tenantID,
			Bucket:       bucket,
			ObjectKey:    in.ObjectKey,
			Key:          key,
			ContentType:  in.ContentType,
			MaxSizeBytes: resolveMaxSize(h.presign.DefaultMaxSize, in.SizeHint),
			ChecksumAlgo: in.ChecksumAlgo,
			TTL:          ttl,
		})
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("presign POST: %w", err))
		}
		out.Method = "POST"
		out.PostAction = action
		out.PostFields = fields
		out.ExpiresAt = exp
	} else {
		url, headers, exp, err := h.storage.PresignPut(ctx, PresignPutArgs{
			TenantID:        tenantID,
			Bucket:          bucket,
			ObjectKey:       in.ObjectKey,
			Key:             key,
			ContentType:     in.ContentType,
			ChecksumAlgo:    in.ChecksumAlgo,
			SizeHint:        in.SizeHint,
			TTL:             ttl,
			RequireChecksum: true,
		})
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("presign PUT: %w", err))
		}
		out.URL = url
		out.Headers = headers
		out.ExpiresAt = exp
	}

	// Capability budget — burns AFTER the row + URL exist so the
	// caller's response is meaningful when charge succeeds. If the
	// charge fails the row is PENDING and the reaper will GC it
	// once presign expires.
	if err := auth.ChargeRequest(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Exemplar RPC: CompleteObject ────────────────────────────────────────────

type CompleteObjectInput struct {
	ObjectKey string
	ObjectID  string
	ETag      string
	Checksum  string
}

// CompleteObject is idempotent: if an event has already promoted the object,
// it returns the current state without error. If the object is still PENDING,
// it HEADs the backend to retrieve authoritative etag/size/sequencer.
func (h *Handler) CompleteObject(ctx context.Context, in CompleteObjectInput) (*Object, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	if in.ObjectKey == "" || in.ObjectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}

	obj, err := h.repo.FindByName(ctx, tenantID, in.ObjectKey, in.ObjectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.ObjectKey + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	principal, _ := auth.PrincipalFromContext(ctx)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: obj.ObjectKey, Key: obj.Key,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes,
	}, cedar.ActionPutObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}

	if obj.State == statemachine.StateAvailable {
		// Already promoted (event-driven). No-op.
		return &obj, nil
	}
	if obj.State != statemachine.StatePending {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot complete in state %s", obj.State))
	}

	// Materialize authoritative values via HEAD against the object's bucket.
	bucket, err := h.repo.LookupBucket(ctx, tenantID, obj.ObjectKey)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	etag, size, checksum, seq, err := h.storage.Head(ctx, bucket, tenantID, obj.ObjectKey, obj.Key)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object not uploaded yet: %w", err))
	}
	if in.ETag != "" && etag != in.ETag {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("etag mismatch"))
	}

	// Promote + outbox fan-out run in ONE transaction (ADR-0003): the
	// `paladin.object.uploaded` rows are written atomically with the
	// PENDING→AVAILABLE flip, so a crash can no longer leave the object
	// AVAILABLE with the event lost (the only notification channel for
	// implicit-mode buckets). The dispatch runs inside onPromoted, which
	// fires only on the real transition; a dispatch error rolls the
	// promote back so the client's at-least-once retry re-promotes and
	// re-emits — consistent, never half-done. Payload is built from the
	// pre-promote read + the authoritative inputs (which are exactly the
	// post-promote etag/size), so no in-tx re-read is needed.
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, obj.ObjectKey)
	changed, err := h.sm.PromoteToAvailableInTx(ctx, obj.ObjectID, etag, size, checksum, seq, statemachine.SourceRPC,
		func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.uploaded",
				objectResourceNameFrom(okPrefix, tenantID, obj.ObjectKey, obj.Key),
				map[string]any{
					"tenant_id":    tenantID.String(),
					"object_key":   obj.ObjectKey,
					"key":          obj.Key,
					"object_id":    obj.ObjectID.String(),
					"size_bytes":   size,
					"etag":         etag,
					"content_type": obj.ContentType,
				})
		})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, in.ObjectKey, in.ObjectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Version history + quota + capability charge fire only on the real
	// transition (retries land changed=false). These remain post-commit:
	// they're separate concerns from the event-atomicity guarantee and a
	// version/quota hiccup must not roll back a delivered event.
	if changed {
		_ = h.versions.OnPromote(ctx, fresh)
		h.touchQuota(ctx, fresh)
		if err := auth.ChargeRequest(ctx); err != nil {
			return nil, err
		}
	}
	return &fresh, nil
}

// ─── ListObjects ────────────────────────────────────────────────────────────

type ListObjectsInput struct {
	ObjectKey string
	PageSize  int32
	PageToken string
	Filter    string
	OrderBy   string
	SortDesc  bool
}

// ListObjects returns a page of objects in the objectKey, optionally filtered by
// a CEL expression against ObjectSchema. Per-row Cedar authorization is
// skipped — listing is permitted for any authenticated tenant member to keep
// pagination cheap (same contract as ListObjectKeys).
func (h *Handler) ListObjects(ctx context.Context, in ListObjectsInput) ([]Object, string, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	// Capability gate: list ops are scoped at the prefix level, so we
	// skip the URI check (empty arg) and only verify the Op caveat.
	// Per-row prefix filtering still happens inside the repo.
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, "", err
	}
	// One tenant+objectKey-scoped Cedar check up front; per-row Cedar would
	// dominate pagination cost.
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: in.ObjectKey,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, "", err
	}
	prog, err := h.filter.Compile(cel.ObjectSchema, in.Filter)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	objs, next, err := h.repo.ListObjects(ctx, ListObjectsArgs{
		TenantID:    tenantID,
		ObjectKey:   in.ObjectKey,
		PageSize:    in.PageSize,
		PageToken:   in.PageToken,
		CompiledCEL: prog,
		Filter:      in.Filter,
		OrderBy:     in.OrderBy,
		SortDesc:    in.SortDesc,
	})
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	return objs, next, nil
}

// ─── CountObjects ───────────────────────────────────────────────────────────

type CountObjectsInput struct {
	ObjectKey string
	Filter    string
}

type CountObjectsOutput struct {
	ApproximateCount int64
	Exact            bool
}

// CountObjects returns the number of objects in the objectKey matching an
// optional CEL filter. With no filter the adapter uses a direct COUNT(*) and
// returns exact=true; with a filter it iterates rows applying CEL and may
// return an approximate result when the scan cap is hit.
func (h *Handler) CountObjects(ctx context.Context, in CountObjectsInput) (*CountObjectsOutput, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: in.ObjectKey,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, err
	}
	prog, err := h.filter.Compile(cel.ObjectSchema, in.Filter)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	// Compile always returns an always-true program for empty expr; detect
	// the "no filter" case at the caller boundary instead, so the adapter
	// can pick the cheap COUNT(*) path.
	args := CountObjectsArgs{TenantID: tenantID, ObjectKey: in.ObjectKey}
	if in.Filter != "" {
		args.CompiledCEL = prog
	}
	n, exact, err := h.repo.CountObjects(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &CountObjectsOutput{ApproximateCount: n, Exact: exact}, nil
}

// ─── ListDistinctTags ─────────────────────────────────────────────────────────

// ListDistinctTags returns the distinct tag key→values across the ObjectKey's
// live objects, for populating a tag-facet filter. Same auth contract as
// ListObjects: an OpList capability caveat plus a single tenant+objectKey Cedar
// check (no per-row authz — the result is an aggregate, not object data).
func (h *Handler) ListDistinctTags(ctx context.Context, objectKey string) (map[string][]string, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: objectKey,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, err
	}
	tags, err := h.repo.ListDistinctTags(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return tags, nil
}

// ─── Read RPCs ──────────────────────────────────────────────────────────────

// GetObject returns metadata for an object addressed by (objectKey, objectID).
//
// Auth chain on this RPC:
//  1. Interceptor stack already verified the caller's JWT and (when
//     present) capability token.
//  2. Capability gate: if the caller presented a capability, it must
//     authorise OpGet on this object's URI. Missing capability → no-op.
//     This runs BEFORE Cedar so a capability holder gets a clean
//     "your capability disallows this op" error rather than a generic
//     "Cedar denied" mask.
//  3. Cedar gate: regardless of capability, the principal's tenant /
//     role / scope must permit GetObject on the resource.
//
// The double gate is intentional: capabilities narrow what an agent can
// do; Cedar enforces tenant-admin policy. Both must agree before the
// read happens.
func (h *Handler) GetObject(ctx context.Context, objectKey, objectID string) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if objectKey == "" || objectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, objectKey, objectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.ObjectKey + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: obj.ObjectKey, Key: obj.Key,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionGetObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	return &obj, nil
}

// LookupObject resolves an object by (object_key, key) instead of object_id —
// useful when callers only have the path-style identifier (S3-style).
func (h *Handler) LookupObject(ctx context.Context, objectKey, key string) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if objectKey == "" || key == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("object_key and key are both required"))
	}
	obj, err := h.repo.FindByPath(ctx, tenantID, objectKey, key)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.ObjectKey + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: obj.ObjectKey, Key: obj.Key,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionGetObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	return &obj, nil
}

// ─── DownloadObject ─────────────────────────────────────────────────────────

type DownloadObjectOutput struct {
	Object             Object
	URL                string
	Headers            map[string]string
	ExpiresAt          time.Time
	ContentDisposition string
}

// DownloadObject returns metadata + a presigned GET URL for an AVAILABLE
// object. PENDING / DELETED / FAILED objects are refused (CodeFailedPrecondition).
func (h *Handler) DownloadObject(ctx context.Context, objectKey, objectID string, ttl time.Duration, disposition string) (*DownloadObjectOutput, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if objectKey == "" || objectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, objectKey, objectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.ObjectKey + "/" + obj.Key
	// Download issues a presigned URL — capability needs OpPresign and
	// OpGet (the underlying op the URL grants). Two assertions, one per
	// caveat axis; either failure short-circuits.
	if err := auth.AssertCapabilityOp(ctx, capability.OpPresign, objectURI); err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return nil, err
	}
	if obj.State != statemachine.StateAvailable {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object state %s does not allow download", obj.State))
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: objectKey, Key: obj.Key,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionPresignGet, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	if ttl <= 0 {
		ttl = h.presign.DefaultTTL
	}
	url, headers, expires, err := h.storage.PresignGet(ctx, PresignGetArgs{
		TenantID:           tenantID,
		Bucket:             bucket,
		ObjectKey:          objectKey,
		Key:                obj.Key,
		TTL:                ttl,
		ContentDisposition: disposition,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("presign get: %w", err))
	}
	return &DownloadObjectOutput{
		Object: obj, URL: url, Headers: headers, ExpiresAt: expires,
		ContentDisposition: disposition,
	}, nil
}

// ─── UpdateObject ───────────────────────────────────────────────────────────

// UpdateObjectInput is the patch shape used by UpdateObject. UpdatedFields
// is the FieldMask: only the named fields are written. Unknown field names
// are silently ignored (matches AIP-134).
type UpdateObjectInput struct {
	ObjectKey       string
	ObjectID        string
	ResourceVersion int64 // 0 = skip OCC
	UpdatedFields   []string
	Metadata        map[string]string
	Tags            map[string]string
	ContentType     string
	ExternalRef     string
}

func (h *Handler) UpdateObject(ctx context.Context, in UpdateObjectInput) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.ObjectKey == "" || in.ObjectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	objectID, err := uuid.Parse(in.ObjectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	objectURI := "object://" + tenantID.String() + "/" + in.ObjectKey + "/"
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: in.ObjectKey,
	}, cedar.ActionUpdateObject, 0, ""); err != nil {
		return nil, err
	}
	// Update + paladin.object.updated fan-out in one tx (ADR-0003): a dispatch
	// failure rolls back the metadata change, so the client's at-least-once
	// retry re-applies both rather than silently dropping the event.
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, in.ObjectKey)
	var obj Object
	err = h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var uerr error
		obj, uerr = h.repo.UpdateMetadataTx(ctx, tx, UpdateMetadataArgs{
			TenantID:        tenantID,
			ObjectID:        objectID,
			ResourceVersion: in.ResourceVersion,
			UpdatedFields:   in.UpdatedFields,
			Metadata:        in.Metadata,
			Tags:            in.Tags,
			ContentType:     in.ContentType,
			ExternalRef:     in.ExternalRef,
		})
		if uerr != nil {
			return uerr
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.updated",
			objectResourceNameFrom(okPrefix, tenantID, obj.ObjectKey, obj.Key),
			map[string]any{
				"tenant_id":      tenantID.String(),
				"object_key":     obj.ObjectKey,
				"key":            obj.Key,
				"object_id":      obj.ObjectID.String(),
				"updated_fields": in.UpdatedFields,
			})
	})
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &obj, nil
}

// ─── DeleteObject (soft + hard) ─────────────────────────────────────────────

// DeleteObject performs a soft-delete by default; permanent=true removes
// the object from S3 first, then drops the row. bypassGovernance opt-in
// honored only for callers holding `lock.governance.bypass` or
// `platform.admin` — protects compliance-mode locks regardless.
func (h *Handler) DeleteObject(ctx context.Context, objectKey, objectIDStr, resourceVersion string, permanent, bypassGovernance bool) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	if objectKey == "" || objectIDStr == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, objectKey, objectIDStr)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + objectKey + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpDelete, objectURI); err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: objectKey, Key: obj.Key,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionDeleteObject, obj.SizeBytes, obj.ContentType); err != nil {
		return err
	}
	rv, err := parseInt64(resourceVersion)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, objectKey)
	if !permanent {
		// Soft-delete + paladin.object.deleted fan-out in one tx (ADR-0003):
		// the event is atomic with the AVAILABLE→DELETED flip.
		err := h.sm.SoftDeleteInTx(ctx, objectID, rv, func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.deleted",
				objectResourceNameFrom(okPrefix, tenantID, obj.ObjectKey, obj.Key),
				map[string]any{
					"tenant_id":  tenantID.String(),
					"object_key": obj.ObjectKey,
					"key":        obj.Key,
					"object_id":  obj.ObjectID.String(),
					"mode":       "soft",
				})
		})
		if err != nil {
			if errors.Is(err, statemachine.ErrConflict) {
				return connect.NewError(connect.CodeAborted, err)
			}
			return connect.NewError(connect.CodeInternal, err)
		}
		// Best-effort delete-marker write; post-commit, separate concern —
		// a marker hiccup must not undo a delivered delete event.
		_ = h.versions.OnSoftDelete(ctx, obj)
		return nil
	}
	// Permanent delete. Ordering matters: drop the DB row FIRST, then
	// the storage bytes. The old order (S3 then DB) could delete the
	// bytes and then fail the DB delete, leaving a live row pointing at
	// nothing — and with the object-lock SQL guard it would even delete
	// a locked object's bytes while the row (correctly) survived. DB
	// first means a failed/blocked delete never touches storage; the
	// only residual failure mode is an orphaned object in S3 (a
	// reclaimable cost leak), never a live row with missing bytes.
	bucket, err := h.repo.LookupBucket(ctx, tenantID, objectKey)
	if err != nil {
		return MapResolveErr(err)
	}

	deleteFn := h.repo.HardDeleteTx
	if bypassGovernance {
		if !principal.HasRole("lock.governance.bypass") && !principal.HasRole("platform.admin") {
			return connect.NewError(connect.CodePermissionDenied,
				errors.New("bypass_governance_retention requires role lock.governance.bypass or platform.admin"))
		}
		deleteFn = h.repo.HardDeleteWithBypassTx
	}

	// Lock pre-check for a clear error. The HardDelete SQL is the
	// non-bypassable safety net (it refuses locked rows even if this
	// check is wrong); this just turns a would-be 0-row "version
	// mismatch" into an accurate FailedPrecondition.
	if lock, lerr := h.repo.ObjectLock(ctx, tenantID, objectID); lerr != nil {
		// FindByName already resolved this object, so a lock-state read
		// error is a genuine DB fault, not a missing row. Log it rather
		// than silently swallow — the HardDelete SQL guard still enforces
		// the lock, so we proceed and let it (and the version check)
		// decide; the caller may see CodeAborted instead of a precise
		// lock reason, which the log explains.
		if h.log != nil {
			h.log.Warn("object lock pre-check failed; relying on SQL guard",
				zap.String("object_id", objectID.String()), zap.Error(lerr))
		}
	} else if lock.Active(time.Now(), bypassGovernance) {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot delete: %s", lock.Reason()))
	}

	// Row removal + paladin.object.deleted fan-out in one tx (ADR-0003): the
	// event is enqueued atomically with the DELETE, so a crash between the
	// two can no longer drop the notification. The S3 byte-removal stays
	// AFTER commit (S3 is non-transactional, and the DB-then-S3 ordering
	// must hold — see the block comment above): once the row is gone the
	// bytes are safe to reclaim, and a failure there only orphans an
	// object, never resurrects a dangling row.
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if derr := deleteFn(ctx, tx, tenantID, objectID, rv); derr != nil {
			return derr
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.deleted",
			objectResourceNameFrom(okPrefix, tenantID, obj.ObjectKey, obj.Key),
			map[string]any{
				"tenant_id":         tenantID.String(),
				"object_key":        obj.ObjectKey,
				"key":               obj.Key,
				"object_id":         obj.ObjectID.String(),
				"mode":              "permanent",
				"bypass_governance": bypassGovernance,
			})
	}); err != nil {
		return apiutil.MapError(err)
	}
	// DB row is gone (and the event is enqueued). Now remove the bytes; a
	// failure here orphans the object in S3 but cannot resurrect a dangling
	// row. Log loudly so a sweeper / operator can reclaim it.
	if err := h.storage.DeleteObject(ctx, bucket, tenantID, objectKey, obj.Key); err != nil {
		if h.log != nil {
			h.log.Error("permanent delete: DB row removed but storage delete failed; object orphaned in S3",
				zap.String("tenant_id", tenantID.String()),
				zap.String("object_key", objectKey),
				zap.String("key", obj.Key),
				zap.String("bucket", bucket),
				zap.Error(err),
			)
		}
	}
	return nil
}

// ─── RestoreObject ──────────────────────────────────────────────────────────

// RestoreObject brings a soft-deleted object back. resourceVersion enforces
// OCC against the row read here — empty skips the check.
// Versioning-aware: when bucket has versioning_enabled, also drops the most
// recent delete-marker before flipping state.
func (h *Handler) RestoreObject(ctx context.Context, objectKey, objectIDStr, resourceVersion string) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if objectKey == "" || objectIDStr == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_key and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, objectKey, objectIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.ObjectKey + "/" + obj.Key
	// Restore is conceptually a Put (re-creates the live object from a
	// soft-deleted row). Capability gate on OpPut.
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: obj.ObjectKey, Key: obj.Key,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionRestoreObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	if obj.State != statemachine.StateDeleted {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot restore from state %s", obj.State))
	}
	// OCC: when caller provided a resource_version, it must match the row
	// we just loaded. TOCTOU-safe enough for restore — concurrent updates
	// on a DELETED row are vanishingly rare (the only mutation paths are
	// sm.Restore itself and HardDelete; both serialize via state guards).
	if resourceVersion != "" {
		expected, err := parseInt64(resourceVersion)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid resource_version: %w", err))
		}
		if expected != obj.ResourceVersion {
			return nil, connect.NewError(connect.CodeAborted,
				fmt.Errorf("resource_version mismatch: expected %d, current %d", expected, obj.ResourceVersion))
		}
	}
	collision, err := h.repo.LiveCollision(ctx, tenantID, objectKey, obj.Key)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if collision {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("a live object already occupies %s/%s", objectKey, obj.Key))
	}
	// Versioning-aware restore: if the parent bucket has versioning_enabled
	// AND the most recent version is a delete-marker, drop that pointer back
	// to the previous non-marker version. This makes RestoreObject a single
	// "make visible again" affordance whether or not versioning is on.
	if err := h.versions.UnsetDeleteMarkerCurrent(ctx, obj); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("restore version pointer: %w", err))
	}
	// Restore + paladin.object.restored fan-out in one tx (ADR-0003). Resource
	// identity (object_key/key/object_id) is unchanged by restore, so the
	// payload is built from the pre-restore `obj`.
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, objectKey)
	if err := h.sm.RestoreInTx(ctx, objectID, func(ctx context.Context, tx pgx.Tx) error {
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.restored",
			objectResourceNameFrom(okPrefix, tenantID, obj.ObjectKey, obj.Key),
			map[string]any{
				"tenant_id":  tenantID.String(),
				"object_key": obj.ObjectKey,
				"key":        obj.Key,
				"object_id":  obj.ObjectID.String(),
			})
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, objectKey, objectIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &fresh, nil
}

// ─── CopyObject ─────────────────────────────────────────────────────────────

// CopyObjectInput identifies a server-side copy. Source is addressed by
// (objectKey, objectID); destination by (object_key, key). When DestKey is
// empty the source's storage key is reused.
type CopyObjectInput struct {
	SourceObjectKey string
	SourceObjectID  string
	DestObjectKey   string
	DestKey         string
	Metadata        map[string]string
	Tags            map[string]string
}

func (h *Handler) CopyObject(ctx context.Context, in CopyObjectInput) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.SourceObjectKey == "" || in.SourceObjectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("source_object_key and source_object_id are required"))
	}
	if in.DestObjectKey == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("dest_object_key is required"))
	}
	src, err := h.repo.FindByName(ctx, tenantID, in.SourceObjectKey, in.SourceObjectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if src.State != statemachine.StateAvailable {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot copy from state %s", src.State))
	}
	destKey := in.DestKey
	if destKey == "" {
		destKey = src.Key
	}
	// Copy = Put on the destination URI (semantically a write of new
	// content) — gated by OpPut. The source must already be readable
	// to the caller; we don't separately gate OpGet on it because the
	// underlying access model treats source-readable-and-dest-writable
	// as the union of the same Cedar policy below.
	destURI := "object://" + tenantID.String() + "/" + in.DestObjectKey + "/" + destKey
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, destURI); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, ObjectKey: in.DestObjectKey, Key: destKey,
		ContentType: src.ContentType, SizeBytes: src.SizeBytes, Tags: in.Tags,
	}, cedar.ActionCopyObject, src.SizeBytes, src.ContentType); err != nil {
		return nil, err
	}

	srcBucket, err := h.repo.LookupBucket(ctx, tenantID, in.SourceObjectKey)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	dstBucket, err := h.repo.LookupBucket(ctx, tenantID, in.DestObjectKey)
	if err != nil {
		return nil, MapResolveErr(err)
	}

	// Insert the destination row up-front so the FK to object_keys is
	// validated before we issue the S3 copy.
	dst, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID:         tenantID,
		ObjectKey:        in.DestObjectKey,
		Key:              destKey,
		ContentType:      src.ContentType,
		SizeHint:         src.SizeBytes,
		ChecksumAlgo:     "", // copied object inherits source's algo via HEAD
		Metadata:         coalesceMap(in.Metadata, src.Metadata),
		Tags:             coalesceMap(in.Tags, src.Tags),
		ExternalRef:      src.ExternalRef,
		PresignExpiresAt: time.Now().Add(h.presign.DefaultTTL),
	})
	if err != nil {
		return nil, mapCreateErr(err)
	}
	if err := h.storage.CopyObject(ctx, Location{
		TenantID: tenantID, Bucket: srcBucket, ObjectKey: in.SourceObjectKey, Key: src.Key,
	}, Location{
		TenantID: tenantID, Bucket: dstBucket, ObjectKey: in.DestObjectKey, Key: destKey,
	}); err != nil {
		// Compensate: the destination row was created PENDING. Without this
		// transition the row would linger forever, since the reconciler only
		// promotes via HEAD against an object that the failed copy never wrote.
		if mfErr := h.sm.MarkFailed(ctx, dst.ObjectID, "storage copy failed"); mfErr != nil {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("storage copy: %w (compensation also failed: %w)", err, mfErr))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("storage copy: %w", err))
	}
	// Promote + paladin.object.uploaded fan-out in one tx (ADR-0003): the
	// copy materialises a brand-new object, so subscribers see the same
	// `paladin.object.uploaded` they'd get from a normal CompleteObject path.
	// The payload's `source` discriminator lets a consumer that cares
	// about origin route copies vs direct uploads. Built from the known
	// inputs (== the post-promote row) so the event can be enqueued inside
	// the promote tx without a read. A dispatch error rolls the promote
	// back; the client's at-least-once retry re-runs both.
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, in.DestObjectKey)
	changed, err := h.sm.PromoteToAvailableInTx(ctx, dst.ObjectID, src.ETag, src.SizeBytes,
		src.Checksum, "", statemachine.SourceRPC,
		func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.uploaded",
				objectResourceNameFrom(okPrefix, tenantID, in.DestObjectKey, destKey),
				map[string]any{
					"tenant_id":         tenantID.String(),
					"object_key":        in.DestObjectKey,
					"key":               destKey,
					"object_id":         dst.ObjectID.String(),
					"size_bytes":        src.SizeBytes,
					"etag":              src.ETag,
					"content_type":      src.ContentType,
					"source":            "copy",
					"source_object_key": in.SourceObjectKey,
					"source_object_id":  in.SourceObjectID,
				})
		})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, in.DestObjectKey, dst.ObjectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if changed {
		_ = h.versions.OnPromote(ctx, fresh)
		h.touchQuota(ctx, fresh)
	}
	return &fresh, nil
}

// ─── shared internals ──────────────────────────────────────────────────────

// touchQuota increments usage counters after a successful promote. Failures
// are logged-only — quota drift gets reconciled by the nightly accounting
// job; a transient pgx error must NOT undo a successful state transition.
func (h *Handler) touchQuota(ctx context.Context, obj Object) {
	if h.quota == nil {
		return
	}
	_ = h.quota.OnObjectPromoted(ctx, obj.TenantID, obj.SizeBytes)
}

func (h *Handler) authorize(
	ctx context.Context,
	principal *auth.Principal,
	tenantID uuid.UUID,
	res *cedar.Resource,
	action string,
	sizeBytes int64,
	contentType string,
) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: principal.Subject, TenantID: tenantID, TenantSlug: principal.TenantSlug, Roles: principal.Roles, Scopes: apiutil.ScopeStrings(principal.Scopes)},
		action,
		res,
		cedar.RequestContext{SizeBytes: sizeBytes, ContentType: contentType, Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func coalesceMap(primary, fallback map[string]string) map[string]string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func resolveMaxSize(defaultMax, hint int64) int64 {
	if hint > 0 && hint < defaultMax {
		return hint
	}
	return defaultMax
}

// pgUniqueViolation is the SQLSTATE code Postgres returns for a unique-index
// conflict. Defined here to avoid pulling in jackc/pgerrcode just for one
// constant.
const pgUniqueViolation = "23505"

func mapCreateErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return connect.NewError(connect.CodeAlreadyExists, err)
		}
	}
	return apiutil.MapError(err)
}

// ErrVersionMismatch is returned by Repository implementations when an
// optimistic-concurrency update fails (resource_version did not match).
var ErrVersionMismatch = errors.New("resource_version mismatch")

// Register this package's sentinels with the central error→Connect-code
// mapper so callers can route through apiutil.MapError for a consistent
// code instead of a hand-written per-handler if/else. errors.Is-based
// matching, so the existing local checks keep working unchanged.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted)
	apiutil.RegisterError(ErrBackendDisabled, connect.CodeFailedPrecondition)
	apiutil.RegisterError(ErrVersionNotFound, connect.CodeNotFound)
}
