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
// Other services (Collection, Presign, Multipart, Batch, Tenant, Operation)
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

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
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
	Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
	CopyObject(ctx context.Context, src, dst Location) error
	// DeleteObject is optional — for permanent deletes only.
	DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error
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
// guarantees a disabled backend processes NO Paladin-mediated operation:
// every object/presign/multipart/copy path resolves its bucket through
// the resolver first, so none can reach the object store. Handlers map
// it to CodeFailedPrecondition via mapResolveErr.
//
// NOTE: this cannot revoke presigned URLs already issued — those hit the
// object store directly, bypassing Paladin, and expire on their own TTL.
// Disabling only blocks issuance of NEW presigns and Paladin-mediated ops.
var ErrBackendDisabled = errors.New("storage backend is disabled")

// ErrBackendReadOnly is returned by the resolution path for a MUTATION
// (write=true) when the resolved backend is in the read-only drain state
// (the schema baseline (001_initial_schema.sql)): enabled, so reads/presign-GET/HEAD/list still resolve,
// but PUT/POST/multipart-init/copy-dest/update/delete/version writes are
// refused so an operator can migrate data off before disabling. Same
// chokepoint + FailedPrecondition mapping as ErrBackendDisabled.
var ErrBackendReadOnly = errors.New("storage backend is read-only (draining)")

// ErrBucketProvisioning is returned by the resolution path for a MUTATION when
// the target bucket exists in the catalog but is not provisioned yet
// (provision_state != 'ready') — the dedicated-bucket window between
// CreateTenant and the reconciler creating the physical bucket (ADR-0011
// Phase 1). Mapped to FailedPrecondition so a client retries once the bucket
// is ready rather than presigning a PUT against a bucket S3 doesn't have.
var ErrBucketProvisioning = errors.New("storage bucket is still provisioning")

// mapResolveErr maps a bucket-resolution error to the right Connect code:
// a disabled/read-only backend or a still-provisioning bucket is
// FailedPrecondition (the resource exists but is not in a state that permits
// the op); anything else is treated as NotFound (the historical behaviour for
// an unresolved object key).
func MapResolveErr(err error) error {
	if errors.Is(err, ErrBackendDisabled) || errors.Is(err, ErrBackendReadOnly) ||
		errors.Is(err, ErrBucketProvisioning) {
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

// Location identifies an S3 object: the physical (backend, bucket) plus the
// composed key (tenant_id/collection/key). Bucket may be empty, in which case
// the storage adapter falls back to its configured default; BackendID likewise
// empty selects the default backend. Together (BackendID, Bucket) are the
// physical-location key the multi-backend routing dispatches on — src and dst
// may differ for a cross-backend copy (docs/backend-registry.md).
type Location struct {
	BackendID  string // storage backend id; "" = default backend
	TenantID   uuid.UUID
	Bucket     string // physical S3 bucket
	Collection string // Paladin namespace within the bucket
	Key        string // storage key inside the prefix
}

type PresignPutArgs struct {
	BackendID       string // storage backend id; "" = default backend
	TenantID        uuid.UUID
	Bucket          string // physical S3 bucket; resolved from Collection row
	Collection      string
	Key             string
	ContentType     string
	ChecksumAlgo    string
	SizeHint        int64
	TTL             time.Duration
	RequireChecksum bool
}

type PresignPostArgs struct {
	BackendID    string // storage backend id; "" = default backend
	TenantID     uuid.UUID
	Bucket       string
	Collection   string
	Key          string
	ContentType  string
	MaxSizeBytes int64
	ChecksumAlgo string
	TTL          time.Duration
}

type PresignGetArgs struct {
	BackendID          string // storage backend id; "" = default backend
	TenantID           uuid.UUID
	Bucket             string
	Collection         string
	Key                string
	TTL                time.Duration
	ContentDisposition string
}

// Repository is the persistence seam. Implementations live in internal/store
// (sqlc-backed). Keeping it local to this package keeps handler tests lean.
type Repository interface {
	CreateObject(ctx context.Context, args CreateObjectArgs) (Object, error)
	FindByName(ctx context.Context, tenantID uuid.UUID, collection, objectID string) (Object, error)
	// FindByIDs returns the rows for the given ids in a single query.
	// Missing ids are simply absent from the result — callers diff
	// against their input to report per-id not-found. Exists so batch
	// executors don't issue one FindByName round-trip per id.
	FindByIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]Object, error)
	FindByPath(ctx context.Context, tenantID uuid.UUID, collection, key string) (Object, error)
	UpdateMetadata(ctx context.Context, args UpdateMetadataArgs) (Object, error)
	// UpdateMetadataTx runs UpdateMetadata on tx so the handler can write
	// the paladin.object.updated outbox rows atomically with the row update
	// (ADR-0003). RunInTx supplies the tx.
	UpdateMetadataTx(ctx context.Context, tx pgx.Tx, args UpdateMetadataArgs) (Object, error)
	ListObjects(ctx context.Context, args ListObjectsArgs) ([]Object, string, error)
	CountObjects(ctx context.Context, args CountObjectsArgs) (count int64, exact bool, err error)
	// ListDistinctTags returns the distinct tag key→values across the
	// Collection's live (non-DELETED) objects, each value list sorted. Backs
	// the tag-facet filter dropdown.
	ListDistinctTags(ctx context.Context, tenantID uuid.UUID, collection string) (map[string][]string, error)
	// LookupBucket returns the storage backend id and the physical S3 bucket
	// for a tenant's Collection. Cheap lookup (covered by
	// idx_collections_bucket_routing). An empty bucket means the row exists
	// but no bucket has been bound — the storage adapter falls back to its
	// configured default in that case. backendID is the physical-location
	// half the multi-backend routing keys on (docs/backend-registry.md);
	// callers that don't route yet may discard it.
	// `write` classifies the operation for the read-only (drain) gate
	// (the schema baseline (001_initial_schema.sql)): pass true for mutations (PUT/POST/multipart-init/
	// copy-dest/delete/version-write), false for reads (GET/HEAD/list). A
	// write against a read-only backend returns ErrBackendReadOnly.
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (backendID, bucket string, err error)
	// LookupBucketMeta returns the bucket binding plus the metadata needed for
	// versioning / lock decisions on the hot path. Implementations should
	// satisfy this with a single query — handlers call it on every promote.
	// `write` gates the read-only drain state as in LookupBucket.
	LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (BucketMeta, error)
	// ObjectLock returns the object row's lock state so the delete path
	// can refuse (and report) a locked object before touching storage.
	ObjectLock(ctx context.Context, tenantID, objectID uuid.UUID) (ObjectLock, error)
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
	// EnqueuePurgeTx records the byte-reclaim debt for a permanent delete on
	// the SAME tx that removes the row. Written before the storage call is
	// ever attempted, so the retry handle is durable even if this process
	// dies immediately after commit — the row it refers to is gone, and
	// nothing else in the schema remembers where its bytes live.
	EnqueuePurgeTx(ctx context.Context, tx pgx.Tx, p PurgeDebt) error
	// SettlePurgeTx clears one debt row. Called on the tx that also emits
	// paladin.object.purged, so "bytes are gone" and "we told anyone" commit
	// together or not at all.
	SettlePurgeTx(ctx context.Context, tx pgx.Tx, purgeID uuid.UUID) error
	// LiveCollision reports whether a non-DELETED row already occupies
	// (tenant, collection, key); used to refuse RestoreObject when the
	// slot has been reused by a fresh upload.
	LiveCollision(ctx context.Context, tenantID uuid.UUID, collection, key string) (bool, error)
}

// PurgeDebt is one permanent delete's byte-reclaim obligation: everything the
// drainer needs to find and remove the bytes after the row that described them
// is gone. Denormalised on purpose — see migrations/001_initial_schema.sql.
type PurgeDebt struct {
	PurgeID    uuid.UUID
	TenantID   uuid.UUID
	ObjectID   uuid.UUID
	BackendID  string
	BucketName string
	Collection string
	Key        string
}

type Object struct {
	ObjectID         uuid.UUID
	TenantID         uuid.UUID
	BackendID        string // FK column from collections; populated when JOINed
	Bucket           string // physical S3 bucket; populated when JOINed
	Collection       string
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
	Collection       string
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
	Collection  string
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
	Collection  string
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
func objectResourceName(tenantID uuid.UUID, collection, key string) string {
	return fmt.Sprintf("tenants/%s/collections/%s/objects-by-key/%s", tenantID, collection, key)
}

// canonicalObjectPrefix resolves the canonical (A-shape) collection prefix
// `storageBackends/{b}/buckets/{bk}/tenants/{tid}/collections/{ok}` used to build
// object-level event resource names (ADR-0010 Phase 1). The (backend, bucket)
// binding depends only on the collection, so callers resolve it ONCE before the
// mutation tx and pass it into the dispatch — never a pool read inside an open
// tx. On a lookup miss it returns "" and the caller falls back to the C-shape
// name; a transient resolve blip must never block the event.
func (h *Handler) canonicalObjectPrefix(ctx context.Context, tenantID uuid.UUID, collection string) string {
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, collection, false) // naming read
	if err != nil || meta.BackendID == "" || meta.BucketName == "" {
		return ""
	}
	return fmt.Sprintf("storageBackends/%s/buckets/%s/tenants/%s/collections/%s",
		meta.BackendID, meta.BucketName, tenantID, collection)
}

// objectResourceNameFrom builds the object event resource name: canonical (A)
// when the pre-resolved prefix is non-empty, else the C-shape fallback. The
// `/objects-by-key/` anchor + user key are appended verbatim (the user key may
// contain '/').
func objectResourceNameFrom(canonicalPrefix string, tenantID uuid.UUID, collection, key string) string {
	if canonicalPrefix == "" {
		return objectResourceName(tenantID, collection, key)
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
	Collection    string
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

	objectURI := "object://" + tenantID.String() + "/" + in.Collection + "/" + in.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}

	// 1. Resolve bucket binding + completion mode in ONE lookup, BEFORE the
	//    Cedar check — the scope-enforcement built-in confines a bucket:/
	//    collection:-scoped PAT to resources whose physical bucket it carries,
	//    so the authz Resource must know its (backend, bucket) or a scoped
	//    principal is fail-closed on this write path. The SAME meta is reused
	//    below for completion mode + presign routing (one query on the hot
	//    upload path), and the lookup keeps the disabled/read-only-backend
	//    chokepoint. LookupBucketMeta joins the same collections ×
	//    storage_backends rows the old BucketCompletionMode + LookupBucket
	//    pair each queried separately. Empty BucketName means "row exists but
	//    bucket_name is NULL" — the storage adapter falls back to its
	//    configured default; after the schema baseline (001_initial_schema.sql) / startup backfill this case
	//    is impossible. It runs before authz: the lookup is tenant-RLS-scoped,
	//    so it only reveals the caller's own tenant's object-key existence
	//    (which MapResolveErr already surfaced pre-scoping).
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, in.Collection, true) // upload (mutation)
	if err != nil {
		return nil, MapResolveErr(err)
	}

	// 2. Derive the object id + key BEFORE authz so the Cedar resource is an
	//    Object entity carrying `key`. The default per-tenant policy reads
	//    resource.key (the .exe/.dll extension blocklist); an ABSENT key makes
	//    the resource a Collection entity → "does not have the attribute key"
	//    eval error → fail-closed deny. A client-omitted key authorizes the
	//    generated object id, which CreateObject persists below (same value).
	objectID := uuid.Must(uuid.NewV7())
	key := in.Key
	if key == "" {
		key = objectID.String()
	}

	// 3. Cedar authorization: may this principal PutObject here?
	principal, _ := auth.PrincipalFromContext(ctx)
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(principal, tenantID),
		cedar.ActionPresignPut,
		&cedar.Resource{
			TenantID:    tenantID,
			Collection:  in.Collection,
			Key:         key,
			BackendID:   meta.BackendID,
			BucketName:  meta.BucketName,
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

	completion := CompletionModeExplicit
	if meta.EventsEnabled {
		completion = CompletionModeImplicit
	}
	bucket := meta.BucketName

	// 4. Insert PENDING row with presign expiry. key was derived before authz
	//    (step 2) so the authorized resource IS the object created.
	ttl := h.presign.DefaultTTL
	presignExp := time.Now().Add(ttl)
	obj, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID:         tenantID,
		Collection:       in.Collection,
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
			BackendID:    meta.BackendID,
			TenantID:     tenantID,
			Bucket:       bucket,
			Collection:   in.Collection,
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
			BackendID:       meta.BackendID,
			TenantID:        tenantID,
			Bucket:          bucket,
			Collection:      in.Collection,
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
	Collection string
	ObjectID   string
	ETag       string
	Checksum   string
}

// CompleteObject is idempotent: if an event has already promoted the object,
// it returns the current state without error. If the object is still PENDING,
// it HEADs the backend to retrieve authoritative etag/size/sequencer.
func (h *Handler) CompleteObject(ctx context.Context, in CompleteObjectInput) (*Object, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	if in.Collection == "" || in.ObjectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}

	obj, err := h.repo.FindByName(ctx, tenantID, in.Collection, in.ObjectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.Collection + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	principal, _ := auth.PrincipalFromContext(ctx)
	// Populate the physical (backend, bucket) on the authz Resource so a
	// bucket:/collection:-scoped PAT enforces on complete. Best-effort +
	// read-only: this RPC is also the idempotent already-AVAILABLE no-op,
	// which historically resolved no bucket, so a resolution failure must NOT
	// newly fail an unscoped completion — leave the bucket empty (scoped
	// principals stay fail-closed as before, unscoped principals are
	// unaffected because the scope-enforcement forbid never fires for them).
	// The PENDING promote path below still resolves with write=true, keeping
	// the disabled/read-only-backend gate exactly where it was.
	authBackendID, authBucket, _ := h.repo.LookupBucket(ctx, tenantID, obj.Collection, false)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: authBackendID, BucketName: authBucket,
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
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, obj.Collection, true) // complete/promote (mutation)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	etag, size, checksum, seq, err := h.storage.Head(ctx, backendID, bucket, tenantID, obj.Collection, obj.Key)
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
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, obj.Collection)
	changed, err := h.sm.PromoteToAvailableInTx(ctx, obj.ObjectID, etag, size, checksum, seq, statemachine.SourceRPC,
		func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.uploaded",
				objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
				map[string]any{
					"tenant_id":    tenantID.String(),
					"collection":   obj.Collection,
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
	fresh, err := h.repo.FindByName(ctx, tenantID, in.Collection, in.ObjectID)
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
	Collection string
	PageSize   int32
	PageToken  string
	Filter     string
	OrderBy    string
	SortDesc   bool
}

// ListObjects returns a page of objects in the collection, optionally filtered by
// a CEL expression against ObjectSchema. Per-row Cedar authorization is
// skipped — listing is permitted for any authenticated tenant member to keep
// pagination cheap (same contract as ListCollections).
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
	// Resolve the collection→bucket binding so a bucket:/collection:-scoped PAT
	// can list within its scope (and is denied off-scope). Best-effort +
	// read-only: an unbound/unknown collection (bucket="" or a lookup error)
	// emits no bucket scope key — unscoped principals are unaffected (the
	// scope-enforcement forbid never fires for them) and scoped principals
	// stay fail-closed. Resolved ONCE here and reused by the per-row loop
	// below (the collection→bucket binding is constant across the page).
	listBackendID, listBucket, _ := h.repo.LookupBucket(ctx, tenantID, in.Collection, false) // list (read)
	// One tenant+collection-scoped Cedar check up front; per-row Cedar would
	// dominate pagination cost.
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.Collection,
		BackendID: listBackendID, BucketName: listBucket,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, "", err
	}
	prog, err := h.filter.Compile(cel.ObjectSchema, in.Filter)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	objs, next, err := h.repo.ListObjects(ctx, ListObjectsArgs{
		TenantID:    tenantID,
		Collection:  in.Collection,
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

	// Per-row Cedar. The up-front check above is collection-scoped, so it can't
	// enforce policies that decide on per-object attributes (tags, state,
	// size, …). When the tenant's applicable policies read such an attribute
	// (analysed once at compile time), re-evaluate each returned object and drop
	// the ones the policy declines. Policies that are constant across the
	// collection take the cheap path and skip this loop entirely.
	//
	// Pagination is unaffected: `next` keys on the last FETCHED row (repo), not
	// the surviving rows — exactly like the CEL filter above — so dropping rows
	// post-fetch keeps the cursor stable and never skips or repeats an object.
	if pe, ok := h.policy.(cedar.PerObjectEvaluator); ok {
		perRow, err := pe.NeedsPerObjectEval(ctx, tenantID, in.Collection)
		if err != nil {
			return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
		}
		if perRow {
			kept := objs[:0]
			for _, o := range objs {
				authErr := h.authorize(ctx, principal, tenantID, &cedar.Resource{
					TenantID:    tenantID,
					Collection:  in.Collection,
					Key:         o.Key,
					ObjectID:    o.ObjectID,
					BackendID:   listBackendID,
					BucketName:  listBucket,
					State:       string(o.State),
					SizeBytes:   o.SizeBytes,
					ContentType: o.ContentType,
					Tags:        o.Tags,
				}, cedar.ActionGetObject, o.SizeBytes, o.ContentType)
				if authErr != nil {
					if connect.CodeOf(authErr) == connect.CodePermissionDenied {
						continue // policy declines this specific object
					}
					return nil, "", authErr // engine fault, not a denial
				}
				kept = append(kept, o)
			}
			objs = kept
		}
	}

	return objs, next, nil
}

// ─── CountObjects ───────────────────────────────────────────────────────────

type CountObjectsInput struct {
	Collection string
	Filter     string
}

type CountObjectsOutput struct {
	ApproximateCount int64
	Exact            bool
}

// CountObjects returns the number of objects in the collection matching an
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
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on count; best-effort + read-only (see ListObjects).
	countBackendID, countBucket, _ := h.repo.LookupBucket(ctx, tenantID, in.Collection, false) // count (read)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.Collection,
		BackendID: countBackendID, BucketName: countBucket,
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
	args := CountObjectsArgs{TenantID: tenantID, Collection: in.Collection}
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

// ListDistinctTags returns the distinct tag key→values across the Collection's
// live objects, for populating a tag-facet filter. Same auth contract as
// ListObjects: an OpList capability caveat plus a single tenant+collection Cedar
// check (no per-row authz — the result is an aggregate, not object data).
func (h *Handler) ListDistinctTags(ctx context.Context, collection string) (map[string][]string, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on the tag facet; best-effort + read-only (see ListObjects).
	tagsBackendID, tagsBucket, _ := h.repo.LookupBucket(ctx, tenantID, collection, false) // list distinct tags (read)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: collection,
		BackendID: tagsBackendID, BucketName: tagsBucket,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, err
	}
	tags, err := h.repo.ListDistinctTags(ctx, tenantID, collection)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return tags, nil
}

// ─── Read RPCs ──────────────────────────────────────────────────────────────

// GetObject returns metadata for an object addressed by (collection, objectID).
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
func (h *Handler) GetObject(ctx context.Context, collection, objectID string) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || objectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.Collection + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding (obj carries no bucket — the find
	// query does not JOIN collections) so bucket:/collection: PAT scopes
	// enforce on read; best-effort + read-only (see ListObjects).
	getBackendID, getBucket, _ := h.repo.LookupBucket(ctx, tenantID, obj.Collection, false) // get (read)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: getBackendID, BucketName: getBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionGetObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	return &obj, nil
}

// LookupObject resolves an object by (collection, key) instead of object_id —
// useful when callers only have the path-style identifier (S3-style).
func (h *Handler) LookupObject(ctx context.Context, collection, key string) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || key == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("collection and key are both required"))
	}
	obj, err := h.repo.FindByPath(ctx, tenantID, collection, key)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.Collection + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, objectURI); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on lookup; best-effort + read-only (see ListObjects).
	lkBackendID, lkBucket, _ := h.repo.LookupBucket(ctx, tenantID, obj.Collection, false) // lookup (read)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: lkBackendID, BucketName: lkBucket,
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
func (h *Handler) DownloadObject(ctx context.Context, collection, objectID string, ttl time.Duration, disposition string) (*DownloadObjectOutput, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || objectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.Collection + "/" + obj.Key
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
	// Resolve the (backend, bucket) BEFORE the Cedar check so a bucket:/
	// collection:-scoped read PAT enforces on download; the same read-only
	// resolution routes the presigned GET below (one lookup, unchanged for
	// the allowed path — it already ran here immediately after authz).
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, collection, false) // download (read)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: collection, Key: obj.Key,
		BackendID: backendID, BucketName: bucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionPresignGet, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = h.presign.DefaultTTL
	}
	url, headers, expires, err := h.storage.PresignGet(ctx, PresignGetArgs{
		BackendID:          backendID,
		TenantID:           tenantID,
		Bucket:             bucket,
		Collection:         collection,
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
	Collection      string
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
	if in.Collection == "" || in.ObjectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(in.ObjectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	objectURI := "object://" + tenantID.String() + "/" + in.Collection + "/"
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on metadata/tag writes (this backs PutObjectTags /
	// DeleteObjectTags). Best-effort + read-only: UpdateObject is a DB-only
	// mutation that historically resolved no bucket, so a resolution failure
	// must NOT newly fail an unscoped update — leave the bucket empty (scoped
	// principals stay fail-closed, unscoped are unaffected).
	updBackendID, updBucket, _ := h.repo.LookupBucket(ctx, tenantID, in.Collection, false) // update (read-only; authz scope only)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.Collection,
		BackendID: updBackendID, BucketName: updBucket,
	}, cedar.ActionUpdateObject, 0, ""); err != nil {
		return nil, err
	}
	// Update + paladin.object.updated fan-out in one tx (ADR-0003): a dispatch
	// failure rolls back the metadata change, so the client's at-least-once
	// retry re-applies both rather than silently dropping the event.
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, in.Collection)
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
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			map[string]any{
				"tenant_id":      tenantID.String(),
				"collection":     obj.Collection,
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
func (h *Handler) DeleteObject(ctx context.Context, collection, objectIDStr, resourceVersion string, permanent, bypassGovernance bool) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	if collection == "" || objectIDStr == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + collection + "/" + obj.Key
	if err := auth.AssertCapabilityOp(ctx, capability.OpDelete, objectURI); err != nil {
		return err
	}
	// Populate the physical (backend, bucket) on the authz Resource so a
	// bucket:/collection:-scoped PAT enforces on delete (soft AND permanent).
	// Best-effort + read-only: the soft-delete path historically resolved no
	// bucket, so a resolution failure must NOT newly fail an unscoped
	// soft-delete — leave the bucket empty (scoped principals stay
	// fail-closed as before, unscoped principals are unaffected). The
	// permanent path below still resolves with write=true, keeping the
	// disabled/read-only-backend gate exactly where it was.
	authBackendID, authBucket, _ := h.repo.LookupBucket(ctx, tenantID, collection, false)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: collection, Key: obj.Key,
		BackendID: authBackendID, BucketName: authBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionDeleteObject, obj.SizeBytes, obj.ContentType); err != nil {
		return err
	}
	rv, err := parseInt64(resourceVersion)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, collection)
	if !permanent {
		// Soft-delete + paladin.object.deleted fan-out in one tx (ADR-0003):
		// the event is atomic with the AVAILABLE→DELETED flip.
		err := h.sm.SoftDeleteInTx(ctx, objectID, rv, func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.deleted",
				objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
				map[string]any{
					"tenant_id":  tenantID.String(),
					"collection": obj.Collection,
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
	backendID, bucket, err := h.repo.LookupBucket(ctx, tenantID, collection, true) // permanent delete (mutation)
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
	// The purge debt is written on this same tx, BEFORE the storage call is
	// attempted. That is the whole fix: the row that says where these bytes
	// live is about to be deleted, and nothing else in the schema records it,
	// so without a durable handle a failed byte-delete leaves bytes that can
	// only be found by listing the bucket. Same outbox discipline as the
	// event above (ADR-0003), one floor down — over bytes instead of
	// notifications.
	debt := PurgeDebt{
		PurgeID:    uuid.New(),
		TenantID:   tenantID,
		ObjectID:   obj.ObjectID,
		BackendID:  backendID,
		BucketName: bucket,
		Collection: collection,
		Key:        obj.Key,
	}
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if derr := deleteFn(ctx, tx, tenantID, objectID, rv); derr != nil {
			return derr
		}
		if perr := h.repo.EnqueuePurgeTx(ctx, tx, debt); perr != nil {
			return perr
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.deleted",
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			map[string]any{
				"tenant_id":         tenantID.String(),
				"collection":        obj.Collection,
				"key":               obj.Key,
				"object_id":         obj.ObjectID.String(),
				"mode":              "permanent",
				"bypass_governance": bypassGovernance,
			})
	}); err != nil {
		return apiutil.MapError(err)
	}

	// Fast path: reclaim the bytes now, so the common case stays synchronous
	// and the debt table stays empty. A failure here is no longer terminal —
	// the debt row survives and worker.PurgeDrainer retries it with backoff.
	if err := h.storage.DeleteObject(ctx, backendID, bucket, tenantID, collection, obj.Key); err != nil {
		if h.log != nil {
			h.log.Warn("permanent delete: storage delete failed; queued for retry",
				zap.String("tenant_id", tenantID.String()),
				zap.String("collection", collection),
				zap.String("key", obj.Key),
				zap.String("bucket", bucket),
				zap.String("purge_id", debt.PurgeID.String()),
				zap.Error(err),
			)
		}
		// Deliberately NOT an error to the caller. The delete IS committed —
		// the row is gone and paladin.object.deleted is enqueued — and the bytes
		// are now owed rather than lost. Returning an error here would tell
		// the client to retry a delete that already succeeded, and its retry
		// would get NotFound. paladin.object.purged is what signals the bytes
		// actually went; it fires from the drainer instead of here.
		return nil
	}

	// Bytes confirmed gone. Clearing the debt and emitting paladin.object.purged
	// in one tx keeps the two facts inseparable: a crash between them leaves
	// the debt row, the drainer re-issues the (idempotent) storage delete,
	// and the event still fires. The alternative — emit, then clear —
	// could announce a purge that never gets recorded as done.
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if serr := h.repo.SettlePurgeTx(ctx, tx, debt.PurgeID); serr != nil {
			return serr
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.purged",
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			map[string]any{
				"tenant_id":  tenantID.String(),
				"collection": obj.Collection,
				"key":        obj.Key,
				"object_id":  obj.ObjectID.String(),
				"backend_id": backendID,
				"bucket":     bucket,
				"reclaimed":  true,
			})
	}); err != nil {
		// The bytes are gone but the bookkeeping did not commit. Harmless and
		// self-correcting: the drainer will retry a DELETE against a key that
		// no longer exists (S3 DELETE is idempotent), succeed, and emit the
		// event then.
		if h.log != nil {
			h.log.Warn("permanent delete: bytes reclaimed but purge bookkeeping failed; drainer will settle",
				zap.String("purge_id", debt.PurgeID.String()),
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
func (h *Handler) RestoreObject(ctx context.Context, collection, objectIDStr, resourceVersion string) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || objectIDStr == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(objectIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	objectURI := "object://" + tenantID.String() + "/" + obj.Collection + "/" + obj.Key
	// Restore is conceptually a Put (re-creates the live object from a
	// soft-deleted row). Capability gate on OpPut.
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, objectURI); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on restore; best-effort + read-only: restore is a DB-only state
	// flip that historically resolved no bucket, so a resolution failure must
	// NOT newly fail an unscoped restore (scoped principals stay fail-closed).
	rsBackendID, rsBucket, _ := h.repo.LookupBucket(ctx, tenantID, obj.Collection, false) // restore (read-only; authz scope only)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: rsBackendID, BucketName: rsBucket,
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
	collision, err := h.repo.LiveCollision(ctx, tenantID, collection, obj.Key)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if collision {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("a live object already occupies %s/%s", collection, obj.Key))
	}
	// Versioning-aware restore: if the parent bucket has versioning_enabled
	// AND the most recent version is a delete-marker, drop that pointer back
	// to the previous non-marker version. This makes RestoreObject a single
	// "make visible again" affordance whether or not versioning is on.
	if err := h.versions.UnsetDeleteMarkerCurrent(ctx, obj); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("restore version pointer: %w", err))
	}
	// Restore + paladin.object.restored fan-out in one tx (ADR-0003). Resource
	// identity (collection/key/object_id) is unchanged by restore, so the
	// payload is built from the pre-restore `obj`.
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, collection)
	if err := h.sm.RestoreInTx(ctx, objectID, func(ctx context.Context, tx pgx.Tx) error {
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.restored",
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			map[string]any{
				"tenant_id":  tenantID.String(),
				"collection": obj.Collection,
				"key":        obj.Key,
				"object_id":  obj.ObjectID.String(),
			})
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &fresh, nil
}

// ─── CopyObject ─────────────────────────────────────────────────────────────

// CopyObjectInput identifies a server-side copy. Source is addressed by
// (collection, objectID); destination by (collection, key). When DestKey is
// empty the source's storage key is reused.
type CopyObjectInput struct {
	SourceCollection string
	SourceObjectID   string
	DestCollection   string
	DestKey          string
	Metadata         map[string]string
	Tags             map[string]string
}

func (h *Handler) CopyObject(ctx context.Context, in CopyObjectInput) (*Object, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.SourceCollection == "" || in.SourceObjectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("source_collection and source_object_id are required"))
	}
	if in.DestCollection == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("dest_collection is required"))
	}
	src, err := h.repo.FindByName(ctx, tenantID, in.SourceCollection, in.SourceObjectID)
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
	destURI := "object://" + tenantID.String() + "/" + in.DestCollection + "/" + destKey
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, destURI); err != nil {
		return nil, err
	}
	// Resolve the DESTINATION (backend, bucket) BEFORE the Cedar check so a
	// bucket:/collection:-scoped write PAT enforces on the copy target — the
	// authz Resource below is the destination (ActionCopyObject is checked
	// against the dest). The same resolution is reused as the copy-dest
	// Location; the source is resolved after authz as before.
	dstBackendID, dstBucket, err := h.repo.LookupBucket(ctx, tenantID, in.DestCollection, true) // copy dest (mutation)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.DestCollection, Key: destKey,
		BackendID: dstBackendID, BucketName: dstBucket,
		ContentType: src.ContentType, SizeBytes: src.SizeBytes, Tags: in.Tags,
	}, cedar.ActionCopyObject, src.SizeBytes, src.ContentType); err != nil {
		return nil, err
	}

	srcBackendID, srcBucket, err := h.repo.LookupBucket(ctx, tenantID, in.SourceCollection, false) // copy source (read)
	if err != nil {
		return nil, MapResolveErr(err)
	}

	// Insert the destination row up-front so the FK to collections is
	// validated before we issue the S3 copy.
	dst, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID:         tenantID,
		Collection:       in.DestCollection,
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
		BackendID: srcBackendID, TenantID: tenantID, Bucket: srcBucket, Collection: in.SourceCollection, Key: src.Key,
	}, Location{
		BackendID: dstBackendID, TenantID: tenantID, Bucket: dstBucket, Collection: in.DestCollection, Key: destKey,
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
	okPrefix := h.canonicalObjectPrefix(ctx, tenantID, in.DestCollection)
	changed, err := h.sm.PromoteToAvailableInTx(ctx, dst.ObjectID, src.ETag, src.SizeBytes,
		src.Checksum, "", statemachine.SourceRPC,
		func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object.uploaded",
				objectResourceNameFrom(okPrefix, tenantID, in.DestCollection, destKey),
				map[string]any{
					"tenant_id":         tenantID.String(),
					"collection":        in.DestCollection,
					"key":               destKey,
					"object_id":         dst.ObjectID.String(),
					"size_bytes":        src.SizeBytes,
					"etag":              src.ETag,
					"content_type":      src.ContentType,
					"source":            "copy",
					"source_collection": in.SourceCollection,
					"source_object_id":  in.SourceObjectID,
				})
		})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, in.DestCollection, dst.ObjectID.String())
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
// are swallowed on purpose: a transient pgx error must NOT undo a committed
// state transition. That makes this path lossy by design, which is safe only
// because worker.QuotaReconciler recomputes usage_total_bytes /
// usage_object_count from live objects on an interval (worker.jobs.
// quota_reconcile). Drop that job and this becomes a counter that only
// climbs — and QuotaSoftCheck rejects uploads against it.
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
		apiutil.CedarPrincipalFor(principal, tenantID),
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
	apiutil.RegisterError(ErrBackendReadOnly, connect.CodeFailedPrecondition)
	apiutil.RegisterError(ErrVersionNotFound, connect.CodeNotFound)
}
