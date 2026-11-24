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
package objecth

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// EventProducer mirrors the seam used by the admin handlers — narrow
// interface, *worker.EventRecorder implements it.
//
// Object lifecycle is the highest-cardinality producer in the
// system: every CompleteObject call fans out, so the per-tenant
// CEL filters on EventSubscriptions become load-bearing for any
// realistic object workload. A subscription with an empty filter
// catches every event class; subscribers that only care about
// `paladin.object.uploaded` should set
//
//	`type == "paladin.object.uploaded"`
//
// (see frontend/src/app/events/page.tsx hint copy) so the
// dispatcher's fan-out doesn't queue rows the consumer would drop anyway.
type EventProducer interface {
	// RecordTx records the event on the caller's transaction, so it is
	// atomic with the state change (ADR-0003); the dispatcher fans it out
	// (ADR-0044).
	RecordTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) error
}

// Storage abstracts S3 / GCS / MinIO. Keep this interface intentionally
// narrow — handler logic does not know which backend it's talking to.
type Storage interface {
	PresignPut(ctx context.Context, args PresignPutArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPost(ctx context.Context, args PresignPostArgs) (action string, fields map[string]string, expiresAt time.Time, err error)
	PresignGet(ctx context.Context, args PresignGetArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	// Head reports the stored object; checksum is its checksum under
	// checksumAlgo, "" when the store keeps none for that algorithm. An
	// object the backend answered is absent is ErrStoredObjectNotFound.
	Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, checksumAlgo string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
	CopyObject(ctx context.Context, src, dst Location) error
	// PublicURL is where an object in a public collection is read unsigned
	// (ADR-0027): under publicBaseURL when set, else the backend's public
	// endpoint.
	PublicURL(ctx context.Context, backendID, bucket, publicBaseURL string, tenantID uuid.UUID, collection, key string) (string, error)
	// DeleteObject is optional — for permanent deletes only.
	DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error
}

// ErrStoredObjectNotFound is Storage.Head's answer when the backend was
// reached and holds nothing at the key. An unreachable or refusing backend is
// a different error: only this one may decide anything terminal.
var ErrStoredObjectNotFound = errors.New("object not found in storage")

// ErrObjectBytesMissing refuses to restore an object whose bytes are gone
// from its bucket: restored, it would be listed and fail every download.
var ErrObjectBytesMissing = errors.New("the object's bytes are gone from its bucket; put them back at its key, or delete it permanently")

// BucketMeta is the minimal projection of bucket metadata the object
// handler needs at promote / delete time. Returned by LookupBucketMeta.
type BucketMeta struct {
	BackendID         string
	BucketName        string
	VersioningEnabled bool
	ObjectLockEnabled bool
	// ObjectLockDefaultMode / DefaultRetention are the bucket-level default
	// the admin plane's SetObjectLock stores. Applied to a version at promote
	// time; empty mode or zero retention means "no default", which is the
	// state of every bucket that has not opted in.
	ObjectLockDefaultMode      string
	ObjectLockDefaultRetention time.Duration
	// EventsEnabled mirrors storage_backends.events_enabled; drives the
	// upload CompletionMode (implicit via bucket events vs explicit
	// CompleteUpload call) without a second lookup on the upload path.
	EventsEnabled bool
	// Constraints are the bucket's upload constraints (BucketService
	// SetConstraints); they narrow limits.* for every object written here.
	Constraints uploadpolicy.BucketConstraints
	// PublicRead: the collection is public (ADR-0027) — the server names its
	// objects, stores them with CacheControl, and gives each a public URL
	// starting at PublicBaseURL ("" for the backend's public endpoint).
	PublicRead    bool
	CacheControl  string
	PublicBaseURL string
}

// ObjectLock is the object row's lock state (object-level, distinct from
// the per-version locks the object_versions trigger enforces).
type ObjectLock struct {
	Mode        string // "", LockModeGovernance or LockModeCompliance
	RetainUntil *time.Time
	LegalHold   bool
}

// The retention modes, as the API and the object_lock_mode SQL enum spell
// them.
const (
	LockModeGovernance = "GOVERNANCE"
	LockModeCompliance = "COMPLIANCE"
)

// RoleLockGovernanceBypass is the role that may weaken or delete through a
// GOVERNANCE retention window.
const RoleLockGovernanceBypass = "lock.governance.bypass"

// msgBypassNeedsRole refuses a governance bypass to a caller without the role.
const msgBypassNeedsRole = "bypass_governance_retention requires role " +
	RoleLockGovernanceBypass + " or " + apiutil.RolePlatformAdmin

// mayBypassGovernance reports whether p holds a role that may bypass a
// GOVERNANCE retention window.
func mayBypassGovernance(p *auth.Principal) bool {
	return p != nil && (p.HasRole(RoleLockGovernanceBypass) || p.HasRole(apiutil.RolePlatformAdmin))
}

// Active reports whether the lock blocks deletion right now. governanceBypass
// only relaxes a GOVERNANCE retention window; legal hold and COMPLIANCE are
// absolute. Mirrors enforce_object_version_lock() and the HardDelete SQL guard.
func (l ObjectLock) Active(now time.Time, governanceBypass bool) bool {
	if l.LegalHold {
		return true
	}
	retained := l.RetainUntil != nil && l.RetainUntil.After(now)
	if l.Mode == LockModeCompliance && retained {
		return true
	}
	if l.Mode == LockModeGovernance && retained && !governanceBypass {
		return true
	}
	return false
}

// activeLock returns the first of locks that is Active, if any.
func activeLock(locks []ObjectLock, now time.Time, governanceBypass bool) (ObjectLock, bool) {
	for _, l := range locks {
		if l.Active(now, governanceBypass) {
			return l, true
		}
	}
	return ObjectLock{}, false
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

// The bucket gate's refusals, under the names the resolution path's callers
// already match on. bucketgate owns them and the state-by-access table that
// decides which one a lookup answers; LookupBucket and LookupBucketMeta return
// them for an operation the bucket's or backend's state does not allow.
var (
	ErrBackendDisabled    = bucketgate.ErrBackendDisabled
	ErrBackendReadOnly    = bucketgate.ErrBackendReadOnly
	ErrBucketProvisioning = bucketgate.ErrBucketProvisioning
)

// MapResolveErr maps a bucket-resolution error to its Connect answer: a gate
// refusal answers the code and reason bucketgate gives it; a collection that
// is not there (apiutil.ErrNotFound) is NotFound; anything else is a failure
// to look it up, which apiutil.MapError answers as the server's — never
// NotFound, which a client would act on.
func MapResolveErr(err error) error {
	return apiutil.MapError(err)
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
	// CacheControl, on a copy's destination in a public collection, is what
	// the copy is stored with, and ContentType is restated with it; both are
	// ignored elsewhere (ADR-0027).
	CacheControl string
	ContentType  string
}

// PresignPutArgs describe the one body a PUT URL will accept: its exact
// size, Content-Type and checksum are all signed into the URL.
type PresignPutArgs struct {
	BackendID     string // storage backend id; "" = default backend
	TenantID      uuid.UUID
	Bucket        string // physical S3 bucket; resolved from Collection row
	Collection    string
	Key           string
	ContentType   string
	SizeBytes     int64
	ChecksumAlgo  string
	ChecksumValue string // base64 digest, see internal/checksum
	// CacheControl is bound into the upload in a public collection
	// (ADR-0027); "" elsewhere.
	CacheControl string
	TTL          time.Duration
}

// PresignPostArgs are PresignPutArgs for a browser form upload: the POST
// policy binds the same size, Content-Type and checksum.
type PresignPostArgs struct {
	BackendID     string // storage backend id; "" = default backend
	TenantID      uuid.UUID
	Bucket        string
	Collection    string
	Key           string
	ContentType   string
	SizeBytes     int64
	ChecksumAlgo  string
	ChecksumValue string
	// CacheControl is bound into the upload in a public collection
	// (ADR-0027); "" elsewhere.
	CacheControl string
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
	// IfMatch, when set, binds the URL to this ETag (see
	// DownloadObjectRequest.require_etag_match).
	IfMatch string
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
	// ListCommonPrefixes returns the folders under args.Prefix, bytewise, at
	// most MaxCommonPrefixes, and whether more exist.
	ListCommonPrefixes(ctx context.Context, args ListPrefixesArgs) ([]string, bool, error)
	CountObjects(ctx context.Context, args CountObjectsArgs) (count int64, exact bool, err error)
	// TallyObjects counts a listing's objects by state and content type
	// together, scanning at most countScanCap rows; Exact is false past it.
	TallyObjects(ctx context.Context, args TallyObjectsArgs) (ObjectTally, error)
	// ListDistinctTags returns the distinct tag key→values across the
	// Collection's live (non-DELETED) objects, each value list sorted. Backs
	// the tag-facet filter dropdown.
	ListDistinctTags(ctx context.Context, tenantID uuid.UUID, collection, afterKey string, keyLimit, valueLimit int32) (DistinctTagPage, error)
	// LookupBucket returns the storage backend id and the physical S3 bucket
	// for a tenant's Collection. Cheap lookup (covered by
	// idx_collections_bucket_routing). An empty bucket means the row exists
	// but no bucket has been bound — the storage adapter falls back to its
	// configured default in that case. backendID is the physical-location
	// half the multi-backend routing keys on (docs/backend-registry.md);
	// callers that don't route yet may discard it.
	// access is what the operation touches, which decides how the bucket's and
	// backend's state answer it (bucketgate's table): a bytes-write against a
	// read-only backend returns ErrBackendReadOnly, a read does not.
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, access bucketgate.Access) (backendID, bucket string, err error)
	// LookupBucketMeta returns the bucket binding plus the metadata needed for
	// versioning / lock decisions on the hot path. Implementations should
	// satisfy this with a single query — handlers call it on every promote.
	// access gates the bucket's and backend's state as in LookupBucket.
	LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, access bucketgate.Access) (BucketMeta, error)
	// ObjectLock returns the object row's lock state so the delete path
	// can refuse (and report) a locked object before touching storage.
	ObjectLock(ctx context.Context, tenantID, objectID uuid.UUID) (ObjectLock, error)
	// VersionLocks returns the lock on every version of the object. A
	// permanent delete removes them all, so the delete pre-check reads these
	// rather than the current version's ObjectLock.
	VersionLocks(ctx context.Context, tenantID, objectID uuid.UUID) ([]ObjectLock, error)
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
	// PurgeBytesTx calls deleteBytes for the bytes at (tenant, collection,
	// key) unless an object uploaded there since owns them, and reports
	// whether the debt is paid — by the delete, or by that object's write
	// replacing them. False leaves the debt for the purge drainer. Run on a
	// tx of its own: it holds the path lock until that tx ends.
	PurgeBytesTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, collection, key string, deleteBytes func() error) (bool, error)
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
	// ObjectState is the deleted row's state: a storage migration counted
	// only live and trashed objects (package purgedebt).
	ObjectState statemachine.State
}

type Object struct {
	ObjectID     uuid.UUID
	TenantID     uuid.UUID
	BackendID    string // FK column from collections; populated when JOINed
	Bucket       string // physical S3 bucket; populated when JOINed
	Collection   string
	Key          string
	State        statemachine.State
	ContentType  string
	SizeBytes    int64
	ETag         string
	ChecksumAlgo string // CRC32C / SHA256 / MD5 — propagated from CreateObjectArgs
	Checksum     string
	// ChecksumPartSizeBytes is set when Checksum is a multipart object's
	// composite: the size of every part but the last. Zero otherwise.
	ChecksumPartSizeBytes int64
	Sequencer             string
	Metadata              map[string]string
	Tags                  map[string]string
	ExternalRef           string
	ResourceVersion       int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CommittedAt           *time.Time
	TerminatedAt          *time.Time
	PresignExpiresAt      *time.Time

	// Lock is the object-lock state of the current version (ADR-0013).
	// Populated on the single-object reads — GetObject, LookupObject — and
	// left zero on ListObjects, where a per-row lock read would add a join to
	// the hot pagination path for a field most deployments never set. Callers
	// that need it for a list read it per object, or use GetObjectLock.
	Lock ObjectLock

	// Taint is the signals the content has been flagged with (taint_handler.go).
	// Empty for a clean object.
	Taint []string

	// PublicURL is where anyone may read the object unsigned, for an object
	// in a public collection (ADR-0027); "" otherwise.
	PublicURL string

	// CreatedByOperation is the operation that created the row (a batch
	// copy), or uuid.Nil.
	CreatedByOperation uuid.UUID

	// Upload is the bucket a PENDING object's upload URL was signed for
	// (objects.upload_bucket_id, migration 113). A storage migration may
	// rebind the collection between the presign and the PUT, and the bytes
	// land where the URL says: CompleteObject looks for them here, and moves
	// them to the collection's bucket. Read by the single-object read the
	// completion makes; nil when the row has none, and once the object is
	// promoted.
	Upload *BucketRef

	// Stored is the bucket the object's bytes are in
	// (objects.placement_bucket_id, ADR-0051), which every read, copy and
	// delete of them resolves; the collection's binding decides only where a
	// new upload is signed. Set by the single-object and batch reads; nil on
	// a PENDING object, whose bytes are confirmed nowhere yet.
	Stored *BucketRef

	// Placement is where the object's bytes physically live. Set only by the
	// read paths (GetObject, LookupObject, ListObjects), and only when Cedar
	// allows the caller ReadObjectPlacement on the object (placement.go);
	// nil everywhere else. The response projection reads this field alone —
	// never BackendID/Bucket above — so an unauthorised caller cannot be
	// shown placement by a path that happened to JOIN the bucket.
	Placement *Placement
}

type CreateObjectArgs struct {
	TenantID    uuid.UUID
	Collection  string
	Key         string
	ContentType string
	// SizeBytes is the size the object is registered with — what policy and
	// quota admitted, and what promotion holds the stored bytes to. nil when
	// the creator does not know it.
	SizeBytes    *int64
	ChecksumAlgo string
	// ChecksumValue is the checksum the object is registered with, base64;
	// "" when the creator has none. Promotion holds the stored bytes to it.
	ChecksumValue    string
	Metadata         map[string]string
	Tags             map[string]string
	ExternalRef      string
	PresignExpiresAt time.Time
	// PublicURL is the object's address in a public collection (ADR-0027),
	// fixed for its life; "" elsewhere.
	PublicURL string
	// CreatedByOperation is the operation creating the row — a batch copy —
	// or uuid.Nil. It is what lets a reissue adopt a row its dead run left
	// PENDING.
	CreatedByOperation uuid.UUID
	// UploadBackendID and UploadBucket name the bucket the upload's URL is
	// signed for, which the row is anchored to (migration 113): a storage
	// migration may rebind the collection before the bytes arrive, and they
	// land where the URL says. Both "" anchor nothing; the bytes are then
	// looked for in the collection's binding.
	UploadBackendID string
	UploadBucket    string
}

type UpdateMetadataArgs struct {
	TenantID        uuid.UUID
	ObjectID        uuid.UUID
	ResourceVersion int64
	UpdatedFields   []string // FieldMetadata, FieldTags, FieldExternalRef
	Metadata        map[string]string
	Tags            map[string]string
	ContentType     string
	ExternalRef     string
}

// The UpdateObject field-mask paths a metadata update writes.
const (
	FieldMetadata    = "metadata"
	FieldTags        = "tags"
	FieldExternalRef = "external_ref"
)

type ListObjectsArgs struct {
	TenantID    uuid.UUID
	Collection  string
	PageSize    int32
	PageToken   string
	CompiledCEL cel.Program // pre-compiled; nil = no filter
	// Filter is the raw CEL expression, kept alongside CompiledCEL so the
	// adapter can extract SQL pushdown predicates (state, key, content type,
	// tag and metadata equality) from it. The
	// CompiledCEL remains authoritative as a post-load pass.
	Filter string
	// Order is the sort, validated by the handler; its page token was minted
	// for it.
	Order keyset.Order
	// Prefix keeps the keys that start with it; empty keeps every key.
	Prefix string
	// Delimiter, when set, keeps only the keys directly under Prefix: none
	// with the delimiter past it. Empty lists every level.
	Delimiter string
}

// ListPrefixesArgs asks for the folders one level under a prefix.
type ListPrefixesArgs struct {
	TenantID   uuid.UUID
	Collection string
	Prefix     string
	Delimiter  string
	// Filter is the list's raw CEL; only the state it names narrows the
	// folders, as in S3, where the other terms of a listing do not.
	Filter string
}

// MaxCommonPrefixes is the most folders one listing returns; more are
// reported as truncated rather than paged.
const MaxCommonPrefixes = 1000

// CountObjectsArgs carries the inputs for Repository.CountObjects. When
// CompiledCEL is nil the adapter can short-circuit to a COUNT(*) query and
// return exact=true; otherwise it iterates the table applying the filter and
// may return an approximate (capped) count.
type CountObjectsArgs struct {
	TenantID    uuid.UUID
	Collection  string
	CompiledCEL cel.Program
	// Filter is the raw CEL behind CompiledCEL, for SQL pushdown — see
	// ListObjectsArgs.Filter. Ignored when CompiledCEL is nil.
	Filter string
}

// Handler is the Connect service implementation.
type Handler struct {
	repo    Repository
	storage Storage
	policy  cedar.Authorizer
	filter  *cel.Evaluator
	sm      StateMachine
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

// StateMachine is the object lifecycle transitioner the handler drives.
// *statemachine.Transitioner is the only production implementation; the
// interface exists because every OTHER collaborator here is already one
// (Repository, Storage, QuotaUpdater, EventProducer) and the concrete type
// made the post-promote tail — version history, quota, capability charge —
// reachable only with a live Postgres pool. pgx.Tx appears in the callbacks
// because the transactional guarantee (ADR-0003) is the point of these two
// methods: the caller's outbox writes must ride the transition's own tx.
type StateMachine interface {
	PromoteToAvailable(ctx context.Context, objectID uuid.UUID, placed *statemachine.Placement, etag string, sizeBytes int64, checksum, sequencer string, source statemachine.Source) (bool, error)
	PromoteToAvailableInTx(ctx context.Context, objectID uuid.UUID, placed *statemachine.Placement, etag string, sizeBytes int64, checksum, sequencer string, source statemachine.Source, onPromoted func(ctx context.Context, tx pgx.Tx) error) (bool, error)
	SoftDeleteInTx(ctx context.Context, objectID uuid.UUID, resourceVersion int64, onDeleted func(ctx context.Context, tx pgx.Tx) error) error
	RestoreInTx(ctx context.Context, objectID uuid.UUID, onRestored func(ctx context.Context, tx pgx.Tx) error) error
	DiscardFailed(ctx context.Context, objectID uuid.UUID, reason statemachine.FailureReason) error
}

// QuotaUpdater is the post-promote accounting hook. Returns nil on missing
// quota — quotas are opt-in. Implementations live in postgres adapters.
type QuotaUpdater interface {
	OnObjectPromoted(ctx context.Context, tenantID, objectID uuid.UUID, sizeBytes int64) error
}

// SetQuotaUpdater attaches the optional usage hook. Wired by main.
func (h *Handler) SetQuotaUpdater(q QuotaUpdater) { h.quota = q }

// SetEventProducer — same opt-in contract as the admin handlers. nil-safe
// via dispatchEvent's guard.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }

// SetLogger sets where each RPC says how it ended; nil keeps the current one.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEventTx records the lifecycle event on the caller's tx so it
// commits atomically with the state transition (ADR-0003). An error
// propagates to the caller, which rolls the transition back — the client's
// at-least-once retry re-runs both. Used by the transactional
// promote / soft-delete / restore paths.
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil {
		return nil
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	err := h.events.RecordTx(ctx, tx, tenantID.String(), worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: resourceName,
		ActorSubject: actor,
		Payload:      payload,
	})
	return err
}

// The keys of an object lifecycle event's payload, and the values that tell
// its variants apart: subscribers read them by name. The keys the purge
// workers emit too come from eventtype, so every producer spells them alike.
const (
	payloadTenantID         = eventtype.PayloadTenantID
	payloadCollection       = eventtype.PayloadCollection
	payloadKey              = eventtype.PayloadKey
	payloadObjectID         = eventtype.PayloadObjectID
	payloadSizeBytes        = "size_bytes"
	payloadETag             = "etag"
	payloadContentType      = "content_type"
	payloadUpdatedFields    = "updated_fields"
	payloadMode             = "mode"
	payloadBypassGovernance = "bypass_governance"
	payloadBackendID        = eventtype.PayloadBackendID
	payloadBucket           = eventtype.PayloadBucket
	payloadReclaimed        = eventtype.PayloadReclaimed
	payloadSource           = eventtype.PayloadSource
	payloadSourceCollection = "source_collection"
	payloadSourceObjectID   = "source_object_id"

	deleteModeSoft      = "soft"
	deleteModePermanent = "permanent"
	uploadSourceCopy    = "copy"
)

// objectEventPayload is an object event's payload: the object it is about,
// plus extra.
func objectEventPayload(tenantID uuid.UUID, collection, key string, objectID uuid.UUID, extra map[string]any) map[string]any {
	p := map[string]any{
		payloadTenantID:   tenantID.String(),
		payloadCollection: collection,
		payloadKey:        key,
		payloadObjectID:   objectID.String(),
	}
	maps.Copy(p, extra)
	return p
}

// objectResourceName is the C-shape (tenant-first) object resource name. Kept
// as the fallback the canonical builders degrade to when the (backend, bucket)
// binding can't be resolved.
func objectResourceName(tenantID uuid.UUID, collection, key string) string {
	return fmt.Sprintf("tenants/%s/collections/%s/objects-by-key/%s", tenantID, collection, key)
}

// canonicalObjectPrefix resolves the canonical (A-shape) collection prefix
// `storageBackends/{b}/buckets/{bk}/tenants/{tid}/collections/{ok}` used to build
// object-level event resource names (ADR-0014 Phase 1). The (backend, bucket)
// binding depends only on the collection, so callers resolve it ONCE before the
// mutation tx and pass it into the dispatch — never a pool read inside an open
// tx. On a lookup miss it returns "" and the caller falls back to the C-shape
// name; a transient resolve blip must never block the event.
func (h *Handler) canonicalObjectPrefix(ctx context.Context, op string, tenantID uuid.UUID, collection string) string {
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, collection, bucketgate.Read) // naming read
	if err != nil {
		warnBestEffort(ctx, h.log, op, readCanonicalPrefix, tenantID, collection, err)
		return ""
	}
	if meta.BackendID == "" || meta.BucketName == "" {
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

// PresignConfig is what the handler needs to admit uploads and mint their
// URLs: how long URLs live, and the global upload limits a bucket's
// constraints narrow.
type PresignConfig struct {
	TTL    presignttl.Policy
	Limits uploadpolicy.Limits
}

func NewHandler(
	repo Repository,
	storage Storage,
	policy cedar.Authorizer,
	filter *cel.Evaluator,
	sm StateMachine,
	cfg PresignConfig,
) *Handler {
	return &Handler{
		repo:    repo,
		storage: storage,
		policy:  policy,
		filter:  filter,
		sm:      sm,
		presign: cfg,
		log:     zap.NewNop(),
	}
}

// ─── Exemplar RPC: UploadObject ─────────────────────────────────────────────
//
// Full flow shown here; other RPCs follow the same shape.

// UploadObjectInput is the decoded request. In production wiring, this comes
// from the generated Connect stub (paladinv1.UploadObjectRequest).
type UploadObjectInput struct {
	Collection  string
	Key         string
	ContentType string
	// SizeBytes is the object's exact size; 0 is an empty object. The URL is
	// signed for exactly this many bytes.
	SizeBytes    int64
	ChecksumAlgo string
	// ChecksumValue is the object's checksum under ChecksumAlgo, base64 of
	// the digest; signed into the URL.
	ChecksumValue string
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
func (h *Handler) UploadObject(ctx context.Context, in UploadObjectInput) (_ *UploadObjectOutput, err error) {
	// Counted like PresignService's presigns, because this is the same
	// transfer by another RPC: the client gets a URL and moves the bytes
	// itself. It was counted only there, so the metric saw a sliver of the
	// uploads and a panel on it read as an idle store.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, uploadPresignOp(in.TransportPOST), metrics.PresignOutcome(err), time.Since(start).Seconds())
		logOutcome(ctx, h.log, opUpload, zapcore.InfoLevel, err, zap.String(fieldCollection, in.Collection))
	}()

	tenantID, err := auth.EffectiveTenant(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := AdmitOptionalKey(FieldKey, in.Key); err != nil {
		return nil, err
	}
	// The checksum is signed into the URL; a malformed one would produce a
	// URL every upload fails against, so it is refused here instead.
	if err := checksum.Validate(in.ChecksumAlgo, in.ChecksumValue); err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("checksum_value: %w", err))
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
	meta, err := h.uploadTarget(ctx, tenantID, &in)
	if err != nil {
		return nil, err
	}
	objectURI := paladin.ObjectResource(tenantID.String(), in.Collection, in.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return nil, err
	}
	policy := uploadpolicy.For(h.presign.Limits, meta.Constraints)
	if err := policy.CheckSingle(uploadpolicy.Upload{
		SizeBytes: in.SizeBytes, ContentType: in.ContentType, ChecksumAlgorithm: in.ChecksumAlgo,
	}); err != nil {
		return nil, err
	}

	// 2. Derive the object id + key BEFORE authz so the Cedar resource is an
	//    Object entity carrying `key`. The default per-tenant policy reads
	//    resource.key (the .exe/.dll extension blocklist); an ABSENT key makes
	//    the resource a Collection entity → "does not have the attribute key"
	//    eval error → fail-closed deny. A client-omitted key authorizes the
	//    generated object id, which CreateObject persists below (same value).
	key := UploadKey(in.Key)

	// 3. Cedar authorization: may this principal PutObject here?
	principal, _ := auth.PrincipalFromContext(ctx)
	if err := apiutil.Decide(ctx, h.policy,
		apiutil.CedarPrincipal(principal),
		cedar.ActionPresignPut,
		&cedar.Resource{
			TenantID:    tenantID,
			Collection:  in.Collection,
			Key:         key,
			BackendID:   meta.BackendID,
			BucketName:  meta.BucketName,
			ContentType: in.ContentType,
			SizeBytes:   in.SizeBytes,
			Tags:        in.Tags,
		},
		cedar.RequestContext{SizeBytes: in.SizeBytes, ContentType: in.ContentType},
	); err != nil {
		return nil, err
	}
	return h.registerUpload(ctx, tenantID, in, meta, policy, key)
}

// uploadPresignOp is the presign metric's op for an upload by PUT or POST.
func uploadPresignOp(post bool) string {
	if post {
		return metrics.PresignOpPost
	}
	return metrics.PresignOpPut
}

// uploadTarget resolves the bucket an upload to in.Collection lands in and
// admits it there: a public collection names the object itself, which is
// what the capability check that follows is of.
func (h *Handler) uploadTarget(ctx context.Context, tenantID uuid.UUID, in *UploadObjectInput) (BucketMeta, error) {
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, in.Collection, bucketgate.BytesWrite) // upload (mutation)
	if err != nil {
		return BucketMeta{}, MapResolveErr(err)
	}
	if err := AdmitPublicUpload(meta, &in.Key, in.ContentType); err != nil {
		return BucketMeta{}, err
	}
	return meta, nil
}

// UploadKey is the key an upload is stored under: the caller's, or a fresh
// id when the caller named none. A multipart upload names its key the same
// way; without the default an empty key would compose to the collection's
// own prefix.
func UploadKey(key string) string {
	if key == "" {
		return uuid.NewV7().String()
	}
	return key
}

// completionMode is how an upload to a bucket completes: by the backend's
// event where it sends them, else by an explicit CompleteObject.
func completionMode(meta BucketMeta) CompletionMode {
	if meta.EventsEnabled {
		return CompletionModeImplicit
	}
	return CompletionModeExplicit
}

// registerUpload inserts the admitted upload's PENDING row, signs its URL and
// charges the capability budget.
func (h *Handler) registerUpload(ctx context.Context, tenantID uuid.UUID, in UploadObjectInput, meta BucketMeta, policy uploadpolicy.Policy, key string) (*UploadObjectOutput, error) {
	// 4. Insert PENDING row with presign expiry. key was derived before authz
	//    (step 2) so the authorized resource IS the object created.
	ttl, err := h.presign.TTL.ResolveWithin(presignttl.OpPut, 0, policy.PutTTLCeiling())
	if err != nil {
		return nil, err
	}
	presignExp := time.Now().Add(ttl)
	publicURL, err := h.publicURL(ctx, meta, tenantID, in.Collection, key)
	if err != nil {
		return nil, err
	}
	obj, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID:         tenantID,
		Collection:       in.Collection,
		Key:              key,
		ContentType:      in.ContentType,
		SizeBytes:        &in.SizeBytes,
		ChecksumAlgo:     in.ChecksumAlgo,
		ChecksumValue:    in.ChecksumValue,
		Metadata:         in.Metadata,
		Tags:             in.Tags,
		ExternalRef:      in.ExternalRef,
		PresignExpiresAt: presignExp,
		PublicURL:        publicURL,
		UploadBackendID:  meta.BackendID,
		UploadBucket:     meta.BucketName,
	})
	if err != nil {
		return nil, mapCreateErr(err)
	}

	// 5. Generate presign. PUT or POST depending on client preference.
	out := &UploadObjectOutput{
		Object:         obj,
		CompletionMode: completionMode(meta),
		Method:         http.MethodPut,
	}
	if err := h.presignUpload(ctx, tenantID, in, meta, key, ttl, out); err != nil {
		return nil, err
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

// presignUpload signs the upload's URL into out: a POST form when the caller
// asked for one, else a PUT.
func (h *Handler) presignUpload(ctx context.Context, tenantID uuid.UUID, in UploadObjectInput, meta BucketMeta, key string, ttl time.Duration, out *UploadObjectOutput) error {
	bucket := meta.BucketName
	if in.TransportPOST {
		action, fields, exp, err := h.storage.PresignPost(ctx, PresignPostArgs{
			BackendID:     meta.BackendID,
			TenantID:      tenantID,
			Bucket:        bucket,
			Collection:    in.Collection,
			Key:           key,
			ContentType:   in.ContentType,
			SizeBytes:     in.SizeBytes,
			ChecksumAlgo:  in.ChecksumAlgo,
			ChecksumValue: in.ChecksumValue,
			TTL:           ttl,
			CacheControl:  meta.CacheControl,
		})
		if err != nil {
			return rpcerr.New(connect.CodeInternal, fmt.Errorf("presign POST: %w", err))
		}
		out.Method = http.MethodPost
		out.PostAction = action
		out.PostFields = fields
		out.ExpiresAt = exp
	} else {
		url, headers, exp, err := h.storage.PresignPut(ctx, PresignPutArgs{
			BackendID:     meta.BackendID,
			TenantID:      tenantID,
			Bucket:        bucket,
			Collection:    in.Collection,
			Key:           key,
			ContentType:   in.ContentType,
			SizeBytes:     in.SizeBytes,
			ChecksumAlgo:  in.ChecksumAlgo,
			ChecksumValue: in.ChecksumValue,
			TTL:           ttl,
			CacheControl:  meta.CacheControl,
		})
		if err != nil {
			return rpcerr.New(connect.CodeInternal, fmt.Errorf("presign PUT: %w", err))
		}
		out.URL = url
		out.Headers = headers
		out.ExpiresAt = exp
	}
	return nil
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
func (h *Handler) CompleteObject(ctx context.Context, in CompleteObjectInput) (_ *Object, err error) {
	defer func() {
		logOutcome(ctx, h.log, opComplete, zapcore.InfoLevel, err, objectFields(in.Collection, in.ObjectID)...)
	}()
	tenantID, err := auth.EffectiveTenant(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := requireObjectName(in.Collection, in.ObjectID); err != nil {
		return nil, err
	}

	obj, err := h.repo.FindByName(ctx, tenantID, in.Collection, in.ObjectID)
	if err != nil {
		return nil, objectLookupError(err)
	}
	objectURI := paladin.ObjectResource(tenantID.String(), obj.Collection, obj.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return nil, err
	}
	principal, _ := auth.PrincipalFromContext(ctx)
	// A completion is a write, the already-AVAILABLE no-op included: it goes
	// through the write gate, so it answers a draining or provisioning bucket
	// as every other write does, and Cedar decides on the resolved binding.
	backendID, bucket, err := writeBinding(ctx, h.repo, tenantID, obj.Collection, bucketgate.BytesWrite)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: backendID, BucketName: bucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes,
	}, cedar.ActionPutObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}

	if obj.State == statemachine.StateAvailable {
		// Already promoted (event-driven). No-op.
		return &obj, nil
	}
	if obj.State != statemachine.StatePending {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"cannot complete in state %s", obj.State)
	}
	return h.completeUpload(ctx, tenantID, in, obj, backendID, bucket)
}

// msgObjectNameRequired refuses a request that does not name its object.
const msgObjectNameRequired = "collection and object_id are required"

// requireObjectName refuses a request that names no collection or no object.
func requireObjectName(collection, objectID string) error {
	if collection == "" || objectID == "" {
		return connect.NewError(connect.CodeInvalidArgument, msgObjectNameRequired)
	}
	return nil
}

// parseObjectName is requireObjectName that also parses the object's id.
func parseObjectName(collection, objectID string) (uuid.UUID, error) {
	if err := requireObjectName(collection, objectID); err != nil {
		return uuid.Nil(), err
	}
	id, err := uuid.Parse(objectID)
	if err != nil {
		return uuid.Nil(), rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	return id, nil
}

// storedBytes is what the backend holds for an object, as a HEAD or a copy
// reports it.
type storedBytes struct {
	etag      string
	size      int64
	checksum  string
	sequencer string
}

// completeUpload promotes a PENDING upload to what the backend holds for it,
// once the caller's claims about those bytes check out. backendID and bucket
// are the binding the write gate resolved for it.
func (h *Handler) completeUpload(ctx context.Context, tenantID uuid.UUID, in CompleteObjectInput, obj Object, backendID, bucket string) (*Object, error) {
	// Materialize authoritative values via HEAD against the object's bucket,
	// once the bytes are there (uploadedBytes).
	bound := Location{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: obj.Collection, Key: obj.Key}
	b, uploadedETag, err := h.uploadedBytes(ctx, obj, bound)
	if err != nil {
		return nil, headFailure(err)
	}
	if err := checkUploadClaims(in, obj, uploadedETag); err != nil {
		return nil, err
	}
	changed, err := h.promoteUploaded(ctx, opComplete, obj.ObjectID, bound, b, map[string]any{
		payloadSizeBytes:   b.size,
		payloadETag:        b.etag,
		payloadContentType: obj.ContentType,
	})
	if err != nil {
		return nil, err
	}
	return h.promotedObject(ctx, tenantID, in.Collection, in.ObjectID, changed)
}

// checkUploadClaims refuses a completion whose caller claims bytes other than
// the ones stored: an ETag the backend does not report, or a checksum other
// than the one the object was registered with — the one its URL was signed
// for. Either means the caller believes it uploaded something it did not.
func checkUploadClaims(in CompleteObjectInput, obj Object, storedETag string) error {
	if in.ETag != "" && storedETag != in.ETag {
		return connect.NewError(connect.CodeFailedPrecondition, "etag mismatch")
	}
	if in.Checksum != "" && obj.Checksum != "" && in.Checksum != obj.Checksum {
		return connect.NewError(connect.CodeFailedPrecondition,
			"checksum_value differs from the checksum the object was registered with")
	}
	return nil
}

// promoteUploaded promotes the PENDING object objectID at loc to the bytes b
// with paladin.object.uploaded in ONE transaction (ADR-0003): the event rows
// are written atomically with the PENDING→AVAILABLE flip, so a crash can no
// longer leave the object AVAILABLE with the event lost (the only
// notification channel for implicit-mode buckets). The dispatch runs inside
// onPromoted, which fires only on the real transition; a dispatch error rolls
// the promote back so the client's at-least-once retry re-promotes and
// re-emits — consistent, never half-done. The payload is the object plus
// extra, built from what the caller already knows, so no in-tx re-read is
// needed. Bytes that break the object's registration are discarded, and a
// promote refused because the collection moved since loc was resolved is
// answered by bindingMoved. It reports whether the promotion was this call's.
func (h *Handler) promoteUploaded(ctx context.Context, op string, objectID uuid.UUID, loc Location, b storedBytes, extra map[string]any) (bool, error) {
	okPrefix := h.canonicalObjectPrefix(ctx, op, loc.TenantID, loc.Collection)
	placed := &statemachine.Placement{BackendID: loc.BackendID, Bucket: loc.Bucket}
	changed, err := h.sm.PromoteToAvailableInTx(ctx, objectID, placed, b.etag, b.size, b.checksum, b.sequencer, statemachine.SourceRPC,
		func(ctx context.Context, tx pgx.Tx) error {
			return h.dispatchEventTx(ctx, tx, loc.TenantID, eventtype.ObjectUploaded,
				objectResourceNameFrom(okPrefix, loc.TenantID, loc.Collection, loc.Key),
				objectEventPayload(loc.TenantID, loc.Collection, loc.Key, objectID, extra))
		})
	if errors.Is(err, statemachine.ErrContentMismatch) {
		return false, h.discardMismatched(ctx, objectID, loc, err)
	}
	if errors.Is(err, statemachine.ErrBindingMoved) {
		return false, h.bindingMoved(ctx, op, objectID, loc, err)
	}
	if err != nil {
		return false, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return changed, nil
}

// bindingMoved answers a promote refused because a storage migration rebound
// the collection after loc was resolved: the bytes are in a bucket the object
// would not be read from. The answer is UNAVAILABLE, and a retry resolves the
// new binding.
//
// A completion's row stays PENDING, anchored to the bucket its URL names, so
// the retry, or the reconciler, moves its bytes into the new binding. A
// copy's row anchors nothing, and its bytes are under the new object's own
// key, which no other row holds: they are deleted and the row discarded, as a
// failed copy's is, and the retry copies again.
func (h *Handler) bindingMoved(ctx context.Context, op string, objectID uuid.UUID, loc Location, cause error) error {
	log := logger.FromContext(ctx).With(zap.String(LogFieldObjectID, objectID.String()),
		zap.String(LogFieldTenantID, loc.TenantID.String()), zap.String(LogFieldCollection, loc.Collection),
		zap.String(LogFieldFromBucket, loc.Bucket), zap.Error(cause))
	refused := connect.NewError(connect.CodeUnavailable, msgBindingMoved).WithCause(cause)
	if op != opCopy {
		log.Warn("promote refused: the collection was rebound since its bucket was resolved; the object stays pending")
		return refused
	}
	log.Warn("promote refused: the collection was rebound since the copy's destination was resolved; discarding the copy")
	if err := h.storage.DeleteObject(ctx, loc.BackendID, loc.Bucket, loc.TenantID, loc.Collection, loc.Key); err != nil {
		log.Error("deleting the copy from the bucket the collection left failed", zap.NamedError("delete", err))
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("%w; deleting the copy failed: %w", cause, err))
	}
	if err := h.sm.DiscardFailed(ctx, objectID, statemachine.FailedStorageCopy); err != nil {
		log.Error("discarding the copy's object failed", zap.NamedError("discard", err))
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("%w; discarding the object failed: %w", cause, err))
	}
	return refused
}

// promotedObject reads back an object just promoted. Version history, quota
// and the capability charge fire only on the real transition (changed), so
// an at-least-once retry neither re-records a version nor double-charges.
// They run after the commit: a version or quota hiccup must not roll back a
// delivered event.
func (h *Handler) promotedObject(ctx context.Context, tenantID uuid.UUID, collection, objectID string, changed bool) (*Object, error) {
	fresh, err := h.repo.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if !changed {
		return &fresh, nil
	}
	LogSideEffect(ctx, EffectVersion, fresh.ObjectID, h.versions.OnPromote(ctx, fresh))
	h.touchQuota(ctx, fresh)
	if err := auth.ChargeRequest(ctx); err != nil {
		return nil, err
	}
	return &fresh, nil
}

// discardMismatched settles an object whose stored bytes broke its
// registration — a size or checksum other than the one admitted. The bytes
// are deleted first and the row discarded second, so a failure between the
// two leaves a PENDING row the reconciler will HEAD and discard, never a row
// gone over bytes nothing will remove. The caller gets FailedPrecondition naming
// what did not match.
func (h *Handler) discardMismatched(ctx context.Context, objectID uuid.UUID, loc Location, cause error) error {
	if err := h.storage.DeleteObject(ctx, loc.BackendID, loc.Bucket, loc.TenantID, loc.Collection, loc.Key); err != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("%w; deleting the stored bytes failed: %w", cause, err))
	}
	if err := h.sm.DiscardFailed(ctx, objectID, statemachine.FailedContentMismatch); err != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("%w; discarding the object failed: %w", cause, err))
	}
	return connect.NewError(connect.CodeFailedPrecondition, cause.Error()).WithCause(cause)
}

// ─── ListObjects ────────────────────────────────────────────────────────────

type ListObjectsInput struct {
	Collection string
	PageSize   int32
	PageToken  string
	Filter     string
	OrderBy    string
	SortDesc   bool
	// Prefix keeps the keys that start with it.
	Prefix string
	// Delimiter lists one level under Prefix; empty lists every level.
	Delimiter string
	// Facets narrow the listing as further conjuncts of Filter.
	Facets ObjectFacets
}

// maxDelimiterRunes is how long a folder delimiter may be: one character,
// as the API declares.
const maxDelimiterRunes = 1

func checkDelimiter(d string) error {
	if utf8.RuneCountInString(d) > maxDelimiterRunes {
		return connect.NewError(connect.CodeInvalidArgument, "delimiter: at most one character")
	}
	return nil
}

// ListCommonPrefixes returns the folders one level under in.Prefix when
// in.Delimiter is set, and nothing otherwise: bytewise, at most
// MaxCommonPrefixes, and whether more exist. The same gates as ListObjects
// hold it, and the filter must compile, though only its state narrows them.
func (h *Handler) ListCommonPrefixes(ctx context.Context, in ListObjectsInput) (_ []string, _ bool, err error) {
	defer func() {
		logOutcome(ctx, h.log, opListFolders, zapcore.DebugLevel, err, zap.String(fieldCollection, in.Collection))
	}()
	in.Filter = withFacets(in.Filter, in.Facets)
	if in.Delimiter == "" {
		return nil, false, nil
	}
	if err := checkDelimiter(in.Delimiter); err != nil {
		return nil, false, err
	}
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, false, err
	}
	// The gates ListObjects holds a listing to: the folders name keys.
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, capabilityCollectionURI(tenantID, in.Collection)); err != nil {
		return nil, false, err
	}
	backendID, bucket := scopeBucket(ctx, h.log, h.repo, opListFolders, tenantID, in.Collection)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.Collection,
		BackendID: backendID, BucketName: bucket,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, false, err
	}
	if _, err := h.filter.Compile(cel.ObjectSchema, in.Filter); err != nil {
		return nil, false, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	folders, truncated, err := h.repo.ListCommonPrefixes(ctx, ListPrefixesArgs{
		TenantID:   tenantID,
		Collection: in.Collection,
		Prefix:     in.Prefix,
		Delimiter:  in.Delimiter,
		Filter:     in.Filter,
	})
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return folders, truncated, nil
}

// ListObjects returns a page of objects in the collection, optionally filtered by
// a CEL expression against ObjectSchema. Per-row Cedar authorization is
// skipped — listing is permitted for any authenticated tenant member to keep
// pagination cheap (same contract as ListCollections).
func (h *Handler) ListObjects(ctx context.Context, in ListObjectsInput) (_ []Object, _ string, err error) {
	defer func() {
		logOutcome(ctx, h.log, opList, zapcore.DebugLevel, err, zap.String(fieldCollection, in.Collection))
	}()
	in.Filter = withFacets(in.Filter, in.Facets)
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, "", err
	}
	order, err := objectOrder(in.OrderBy, in.SortDesc)
	if err != nil {
		return nil, "", err
	}
	if err := checkDelimiter(in.Delimiter); err != nil {
		return nil, "", err
	}
	// Capability gate: a listing reads the whole collection, so it is
	// asserted against the collection's URI prefix. A capability confined to
	// part of the collection is refused rather than shown every row's name —
	// nothing filters rows by resource caveat, and an empty URI here used to
	// skip the resource check entirely.
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, capabilityCollectionURI(tenantID, in.Collection)); err != nil {
		return nil, "", err
	}
	// Resolve the collection→bucket binding so a bucket:/collection:-scoped PAT
	// can list within its scope (and is denied off-scope). Best-effort +
	// read-only: an unbound/unknown collection (bucket="" or a lookup error)
	// emits no bucket scope key — unscoped principals are unaffected (the
	// scope-enforcement forbid never fires for them) and scoped principals
	// stay fail-closed. Resolved ONCE here and reused by the per-row loop
	// below (the collection→bucket binding is constant across the page).
	listBackendID, listBucket := scopeBucket(ctx, h.log, h.repo, opList, tenantID, in.Collection)
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
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	objs, next, err := h.repo.ListObjects(ctx, ListObjectsArgs{
		TenantID:    tenantID,
		Collection:  in.Collection,
		PageSize:    in.PageSize,
		PageToken:   in.PageToken,
		CompiledCEL: prog,
		Filter:      in.Filter,
		Order:       order,
		Prefix:      in.Prefix,
		Delimiter:   in.Delimiter,
	})
	if err != nil {
		return nil, "", listError(err)
	}
	objs, err = h.authorizeRows(ctx, principal, tenantID, in.Collection, listBackendID, listBucket, objs)
	if err != nil {
		return nil, "", err
	}
	return objs, next, nil
}

// listError answers a listing the store refused: a page token that is
// malformed or minted for another order is the caller's; anything else is
// the store failing.
func listError(err error) error {
	if errors.Is(err, keyset.ErrMalformed) || errors.Is(err, keyset.ErrOrderChanged) {
		return connect.NewError(connect.CodeInvalidArgument, "page_token: "+err.Error()).WithCause(err)
	}
	return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
}

// authorizeRows drops the rows of a page the caller's policies decline and
// attaches placement to those it may see it for.
func (h *Handler) authorizeRows(ctx context.Context, principal *auth.Principal, tenantID uuid.UUID, collection, listBackendID, listBucket string, objs []Object) ([]Object, error) {
	// Per-row Cedar. ListObjects' up-front check is collection-scoped, so it can't
	// enforce policies that decide on per-object attributes (tags, state,
	// size, …). When the tenant's applicable policies read such an attribute
	// (analysed once at compile time), re-evaluate each returned object and drop
	// the ones the policy declines. Policies that are constant across the
	// collection take the cheap path and skip this loop entirely.
	//
	// Pagination is unaffected: `next` keys on the last FETCHED row (repo), not
	// the surviving rows — exactly like the CEL filter above — so dropping rows
	// post-fetch keeps the cursor stable and never skips or repeats an object.
	pe, ok := h.policy.(cedar.PerObjectEvaluator)
	if !ok {
		h.attachListPlacement(ctx, principal, tenantID, collection, listBackendID, listBucket, objs)
		return objs, nil
	}
	perRow, err := pe.NeedsPerObjectEval(ctx, tenantID, collection)
	if err != nil {
		return nil, apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if !perRow {
		h.attachListPlacement(ctx, principal, tenantID, collection, listBackendID, listBucket, objs)
		return objs, nil
	}
	return h.authorizeEachRow(ctx, principal, tenantID, collection, listBackendID, listBucket, objs)
}

// authorizeEachRow re-evaluates the policies for each object of a page,
// keeping those they allow, with placement decided per object too.
func (h *Handler) authorizeEachRow(ctx context.Context, principal *auth.Principal, tenantID uuid.UUID, collection, listBackendID, listBucket string, objs []Object) ([]Object, error) {
	kept := objs[:0]
	for _, o := range objs {
		rowRes := &cedar.Resource{
			TenantID:    tenantID,
			Collection:  collection,
			Key:         o.Key,
			ObjectID:    o.ObjectID,
			BackendID:   listBackendID,
			BucketName:  listBucket,
			State:       string(o.State),
			SizeBytes:   o.SizeBytes,
			ContentType: o.ContentType,
			Tags:        o.Tags,
		}
		authErr := h.authorize(ctx, principal, tenantID, rowRes, cedar.ActionGetObject, o.SizeBytes, o.ContentType)
		if connect.CodeOf(authErr) == connect.CodePermissionDenied {
			continue // policy declines this specific object
		}
		if authErr != nil {
			return nil, authErr // engine fault, not a denial
		}
		// The policies decide per object, so placement does too.
		h.attachPlacement(ctx, principal, rowRes, &o)
		kept = append(kept, o)
	}
	return kept, nil
}

// attachListPlacement sets Placement on every row of a page with ONE
// collection-scoped ReadObjectPlacement check, the same shape as the page's
// GetObject check above. Sound only when the applicable policies are constant
// across the collection (NeedsPerObjectEval false); otherwise the per-row loop
// decides placement per object instead.
func (h *Handler) attachListPlacement(ctx context.Context, principal *auth.Principal, tenantID uuid.UUID, collection, backendID, bucket string, objs []Object) {
	if len(objs) == 0 {
		return
	}
	if !h.mayReadPlacement(ctx, principal, &cedar.Resource{
		TenantID: tenantID, Collection: collection,
		BackendID: backendID, BucketName: bucket,
	}, 0, "") {
		return
	}
	for i := range objs {
		objs[i].Placement = placementFor(ctx, tenantID, &objs[i])
	}
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
func (h *Handler) CountObjects(ctx context.Context, in CountObjectsInput) (_ *CountObjectsOutput, err error) {
	defer func() {
		logOutcome(ctx, h.log, opCount, zapcore.DebugLevel, err, zap.String(fieldCollection, in.Collection))
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, capabilityCollectionURI(tenantID, in.Collection)); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on count; best-effort + read-only (see ListObjects).
	countBackendID, countBucket := scopeBucket(ctx, h.log, h.repo, opCount, tenantID, in.Collection)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.Collection,
		BackendID: countBackendID, BucketName: countBucket,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, err
	}
	prog, err := h.filter.Compile(cel.ObjectSchema, in.Filter)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	// Compile always returns an always-true program for empty expr; detect
	// the "no filter" case at the caller boundary instead, so the adapter
	// can pick the cheap COUNT(*) path.
	args := CountObjectsArgs{TenantID: tenantID, Collection: in.Collection}
	if in.Filter != "" {
		args.CompiledCEL = prog
		args.Filter = in.Filter
	}
	n, exact, err := h.repo.CountObjects(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &CountObjectsOutput{ApproximateCount: n, Exact: exact}, nil
}

// DistinctTagPage is one page of tag facets: keys in ascending order, each
// with its (possibly capped) value list.
type DistinctTagPage struct {
	// Keys, ascending — the order the cursor advances through.
	Keys []string
	// Values per key. Truncated marks a key whose value list hit ValueLimit
	// and has more behind it.
	Values    map[string][]string
	Truncated map[string]bool
	// NextKey is the cursor for the following page; empty when exhausted.
	NextKey string
}

// distinctTagValueLimit caps how many values come back per tag key. The facet
// UI this feeds shows a filter list, not a full inventory; a key with more
// values than this is marked truncated so the caller knows it is seeing a
// sample rather than the set.
const distinctTagValueLimit = 100

// ─── ListDistinctTags ─────────────────────────────────────────────────────────

// ListDistinctTags returns the distinct tag key→values across the Collection's
// live objects, for populating a tag-facet filter. Same auth contract as
// ListObjects: an OpList capability caveat plus a single tenant+collection Cedar
// check (no per-row authz — the result is an aggregate, not object data).
func (h *Handler) ListDistinctTags(
	ctx context.Context, collection, pageToken string, pageSize int32,
) (_ DistinctTagPage, err error) {
	defer func() {
		logOutcome(ctx, h.log, opListTags, zapcore.DebugLevel, err, zap.String(fieldCollection, collection))
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return DistinctTagPage{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, capabilityCollectionURI(tenantID, collection)); err != nil {
		return DistinctTagPage{}, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on the tag facet; best-effort + read-only (see ListObjects).
	tagsBackendID, tagsBucket := scopeBucket(ctx, h.log, h.repo, opListTags, tenantID, collection)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: collection,
		BackendID: tagsBackendID, BucketName: tagsBucket,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return DistinctTagPage{}, err
	}
	page, err := h.repo.ListDistinctTags(ctx, tenantID, collection, pageToken, pageSize, distinctTagValueLimit)
	if err != nil {
		return DistinctTagPage{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return page, nil
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
func (h *Handler) GetObject(ctx context.Context, collection, objectID string) (_ *Object, err error) {
	defer func() { logOutcome(ctx, h.log, opGet, zapcore.DebugLevel, err, objectFields(collection, objectID)...) }()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || objectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "collection and object_id are required")
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, objectLookupError(err)
	}
	objectURI := paladin.ObjectResource(tenantID.String(), obj.Collection, obj.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, objectURI); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding (obj carries no bucket — the find
	// query does not JOIN collections) so bucket:/collection: PAT scopes
	// enforce on read; best-effort + read-only (see ListObjects).
	getBackendID, getBucket := scopeBucket(ctx, h.log, h.repo, opGet, tenantID, obj.Collection)
	getRes := &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: getBackendID, BucketName: getBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}
	if err := h.authorize(ctx, principal, tenantID, getRes, cedar.ActionGetObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	h.attachLock(ctx, tenantID, &obj)
	h.attachPlacement(ctx, principal, getRes, &obj)
	return &obj, nil
}

// attachLock fills Object.Lock for a single-object read. Best-effort by
// design: the lock is descriptive here, not an authorisation input — the
// delete path reads it again through its own pre-check, and the SQL guard and
// the object_locks trigger enforce it regardless of what this returns. A
// failure here must not turn a successful GetObject into an error.
func (h *Handler) attachLock(ctx context.Context, tenantID uuid.UUID, obj *Object) {
	lock, err := h.repo.ObjectLock(ctx, tenantID, obj.ObjectID)
	if err != nil {
		logger.FromContext(ctx).Warn("object lock read failed; reporting object without lock state",
			zap.String("object_id", obj.ObjectID.String()), zap.Error(err))
		return
	}
	obj.Lock = lock
}

// LookupObject resolves an object by (collection, key) instead of object_id —
// useful when callers only have the path-style identifier (S3-style).
func (h *Handler) LookupObject(ctx context.Context, collection, key string) (out *Object, err error) {
	defer func() {
		fields := []zap.Field{zap.String(fieldCollection, collection)}
		if out != nil {
			fields = append(fields, zap.String(fieldObjectID, out.ObjectID.String()))
		}
		logOutcome(ctx, h.log, opLookup, zapcore.DebugLevel, err, fields...)
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || key == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			"collection and key are both required")
	}
	obj, err := h.repo.FindByPath(ctx, tenantID, collection, key)
	if err != nil {
		return nil, objectLookupError(err)
	}
	objectURI := paladin.ObjectResource(tenantID.String(), obj.Collection, obj.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, objectURI); err != nil {
		return nil, err
	}
	// Resolve the collection→bucket binding so bucket:/collection: PAT scopes
	// enforce on lookup; best-effort + read-only (see ListObjects).
	lkBackendID, lkBucket := scopeBucket(ctx, h.log, h.repo, opLookup, tenantID, obj.Collection)
	lkRes := &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: lkBackendID, BucketName: lkBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}
	if err := h.authorize(ctx, principal, tenantID, lkRes, cedar.ActionGetObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	h.attachLock(ctx, tenantID, &obj)
	h.attachPlacement(ctx, principal, lkRes, &obj)
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
// object. PENDING / DELETED objects are refused (CodeFailedPrecondition).
// DownloadObject returns the object and a presigned GET for it.
// requireETagMatch binds the URL to the object's current ETag.
func (h *Handler) DownloadObject(ctx context.Context, collection, objectID string, ttl time.Duration, disposition string, requireETagMatch bool) (_ *DownloadObjectOutput, err error) {
	// See UploadObject: a download URL is a presign whichever RPC mints it.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, metrics.PresignOpGet, metrics.PresignOutcome(err), time.Since(start).Seconds())
		logOutcome(ctx, h.log, opDownload, zapcore.DebugLevel, err, objectFields(collection, objectID)...)
	}()

	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	disposition, err = checkDownloadInput(collection, objectID, disposition)
	if err != nil {
		return nil, err
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, objectLookupError(err)
	}
	objectURI := paladin.ObjectResource(tenantID.String(), obj.Collection, obj.Key)
	// Download issues a presigned URL — capability needs OpPresign and
	// OpGet (the underlying op the URL grants). Two assertions, one per
	// caveat axis; either failure short-circuits.
	if err := auth.AssertCapabilityOp(ctx, auth.OpPresign, objectURI); err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, objectURI); err != nil {
		return nil, err
	}
	meta, ttl, err := h.downloadTarget(ctx, tenantID, collection, obj, ttl)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: collection, Key: obj.Key,
		BackendID: meta.BackendID, BucketName: meta.BucketName,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionPresignGet, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	return h.presignDownload(ctx, tenantID, collection, obj, ttl, disposition, requireETagMatch)
}

// checkDownloadInput refuses a download that does not name its object, and
// normalises the disposition it asks the URL to carry.
func checkDownloadInput(collection, objectID, disposition string) (string, error) {
	if err := requireObjectName(collection, objectID); err != nil {
		return "", err
	}
	return NormalizeContentDisposition(disposition)
}

// downloadTarget refuses an object with no bytes to serve, and resolves the
// (backend, bucket) BEFORE the Cedar check so a bucket:/collection:-scoped
// read PAT enforces on download; the same read-only resolution routes the
// presigned GET, whose lifetime the bucket's constraints bound.
func (h *Handler) downloadTarget(ctx context.Context, tenantID uuid.UUID, collection string, obj Object, ttl time.Duration) (BucketMeta, time.Duration, error) {
	if obj.State != statemachine.StateAvailable {
		return BucketMeta{}, 0, connect.Errorf(connect.CodeFailedPrecondition,
			"object state %s does not allow download", obj.State)
	}
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, collection, bucketgate.Read) // download (read)
	if err != nil {
		return BucketMeta{}, 0, MapResolveErr(err)
	}
	ttl, err = h.presign.TTL.ResolveWithin(presignttl.OpGet, ttl,
		uploadpolicy.For(h.presign.Limits, meta.Constraints).GetTTLCeiling())
	if err != nil {
		return BucketMeta{}, 0, err
	}
	return meta, ttl, nil
}

// presignDownload signs an authorized download's GET URL. requireETagMatch
// binds it to the object's current ETag.
func (h *Handler) presignDownload(ctx context.Context, tenantID uuid.UUID, collection string, obj Object, ttl time.Duration, disposition string, requireETagMatch bool) (*DownloadObjectOutput, error) {
	// Signed for the bucket the bytes are in, which the collection's binding
	// need not be (ADR-0051).
	at, err := StoredAt(obj, bucketgate.Read) // download (read)
	if err != nil {
		return nil, MapResolveErr(err)
	}
	args := PresignGetArgs{
		BackendID:          at.BackendID,
		TenantID:           tenantID,
		Bucket:             at.Bucket,
		Collection:         collection,
		Key:                obj.Key,
		TTL:                ttl,
		ContentDisposition: disposition,
	}
	if requireETagMatch {
		if obj.ETag == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				"the object has no recorded ETag to bind the URL to")
		}
		args.IfMatch = obj.ETag
	}
	url, headers, expires, err := h.storage.PresignGet(ctx, args)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("presign get: %w", err))
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

func (h *Handler) UpdateObject(ctx context.Context, in UpdateObjectInput) (_ *Object, err error) {
	defer func() {
		logOutcome(ctx, h.log, opUpdate, zapcore.InfoLevel, err, objectFields(in.Collection, in.ObjectID)...)
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	objectID, cur, err := h.updateTarget(ctx, tenantID, in)
	if err != nil {
		return nil, err
	}
	objectURI := paladin.ObjectResource(tenantID.String(), in.Collection, cur.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return nil, err
	}
	resource, err := h.updateResource(ctx, tenantID, in.Collection, cur)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, resource,
		cedar.ActionUpdateObject, cur.SizeBytes, cur.ContentType); err != nil {
		return nil, err
	}
	if err := takesMetadataUpdate(cur); err != nil {
		return nil, err
	}
	obj, err := h.writeMetadata(ctx, tenantID, objectID, in)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &obj, nil
}

// updateTarget resolves the object an update names. The update authorizes
// against the object itself, so a policy on its key, tags or content type
// applies to an update the same way it does to a delete.
func (h *Handler) updateTarget(ctx context.Context, tenantID uuid.UUID, in UpdateObjectInput) (uuid.UUID, Object, error) {
	objectID, err := objectRef(in.Collection, in.ObjectID)
	if err != nil {
		return uuid.Nil(), Object{}, err
	}
	cur, err := h.repo.FindByName(ctx, tenantID, in.Collection, in.ObjectID)
	if err != nil {
		return uuid.Nil(), Object{}, objectLookupError(err)
	}
	return objectID, cur, nil
}

// updateResource is the Cedar resource an update of cur, in collection, is
// decided on.
//
// It resolves the collection→bucket binding through the write gate, so
// bucket:/collection: PAT scopes enforce on metadata/tag writes (this backs
// PutObjectTags / DeleteObjectTags). An update changes only the object's row,
// so it is a metadata write: a draining backend takes it.
func (h *Handler) updateResource(ctx context.Context, tenantID uuid.UUID, collection string, cur Object) (*cedar.Resource, error) {
	backendID, bucket, err := writeBinding(ctx, h.repo, tenantID, collection, bucketgate.MetadataWrite)
	if err != nil {
		return nil, err
	}
	return &cedar.Resource{
		TenantID: tenantID, Collection: collection, Key: cur.Key,
		BackendID: backendID, BucketName: bucket,
		ContentType: cur.ContentType, SizeBytes: cur.SizeBytes, Tags: cur.Tags,
	}, nil
}

// takesMetadataUpdate refuses an update of an object that is not AVAILABLE.
// Asking the store anyway matched no row and came back as a version
// conflict, which told the caller to reload and retry an update that could
// never land.
func takesMetadataUpdate(cur Object) error {
	if cur.State != statemachine.StateAvailable {
		return connect.Errorf(connect.CodeFailedPrecondition,
			"object state %s takes no metadata update", cur.State)
	}
	return nil
}

// objectRef validates the (collection, object_id) pair a request names and
// parses the id.
func objectRef(collection, objectID string) (uuid.UUID, error) {
	if collection == "" || objectID == "" {
		return uuid.Nil(), connect.NewError(connect.CodeInvalidArgument, "collection and object_id are required")
	}
	id, err := uuid.Parse(objectID)
	if err != nil {
		return uuid.Nil(), rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	return id, nil
}

// writeMetadata applies UpdateObject's change and fans out
// paladin.object.updated in one tx (ADR-0003): a dispatch failure rolls back
// the metadata change, so the client's at-least-once retry re-applies both
// rather than silently dropping the event.
func (h *Handler) writeMetadata(ctx context.Context, tenantID, objectID uuid.UUID, in UpdateObjectInput) (Object, error) {
	okPrefix := h.canonicalObjectPrefix(ctx, opUpdate, tenantID, in.Collection)
	var obj Object
	err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
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
		return h.dispatchEventTx(ctx, tx, tenantID, eventtype.ObjectUpdated,
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			objectEventPayload(tenantID, obj.Collection, obj.Key, obj.ObjectID, map[string]any{
				payloadUpdatedFields: in.UpdatedFields,
			}))
	})
	return obj, err
}

// ─── DeleteObject (soft + hard) ─────────────────────────────────────────────

// DeleteObject performs a soft-delete by default; permanent=true removes
// the object from S3 first, then drops the row. bypassGovernance opt-in
// honored only for callers holding `lock.governance.bypass` or
// `platform.admin` — protects compliance-mode locks regardless.
func (h *Handler) DeleteObject(ctx context.Context, collection, objectIDStr, resourceVersion string, permanent, bypassGovernance bool) (err error) {
	defer func() {
		logOutcome(ctx, h.log, opDelete, zapcore.InfoLevel, err,
			append(objectFields(collection, objectIDStr), zap.Bool("permanent", permanent))...)
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return err
	}
	objectID, err := parseObjectName(collection, objectIDStr)
	if err != nil {
		return err
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return objectLookupError(err)
	}
	objectURI := paladin.ObjectResource(tenantID.String(), collection, obj.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpDelete, objectURI); err != nil {
		return err
	}
	// A delete goes through the write gate, and Cedar decides on the binding
	// it resolves. A move to the trash changes only the row; a permanent
	// delete removes bytes, and PermanentDelete resolves it as such again.
	authBackendID, authBucket, err := writeBinding(ctx, h.repo, tenantID, collection, bucketgate.DeleteAccess(permanent))
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: collection, Key: obj.Key,
		BackendID: authBackendID, BucketName: authBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionDeleteObject, obj.SizeBytes, obj.ContentType); err != nil {
		return err
	}
	return h.deleteAuthorized(ctx, tenantID, principal, objectID, obj, resourceVersion, permanent, bypassGovernance)
}

// deleteAuthorized carries out a delete DeleteObject has authorized: to the
// trash, or for good when permanent is set.
func (h *Handler) deleteAuthorized(
	ctx context.Context, tenantID uuid.UUID, principal *auth.Principal, objectID uuid.UUID, obj Object,
	resourceVersion string, permanent, bypassGovernance bool,
) error {
	rv, err := parseInt64(resourceVersion)
	if err != nil {
		return rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	if !permanent {
		return h.softDelete(ctx, tenantID, obj.Collection, objectID, rv, obj)
	}
	if err := h.assertBypassAllowed(principal, bypassGovernance); err != nil {
		return err
	}
	return h.PermanentDelete(ctx, tenantID, obj, rv, bypassGovernance)
}

// msgPublicHasNoTrash refuses a soft delete in a public collection.
const msgPublicHasNoTrash = "a public object is deleted with permanent=true; it has no trash"

// softDelete moves an authorized object to the trash with
// paladin.object.deleted on the same transaction (ADR-0003).
func (h *Handler) softDelete(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID, rv int64, obj Object) error {
	okPrefix := h.canonicalObjectPrefix(ctx, opDelete, tenantID, collection)
	if obj.PublicURL != "" {
		// A public collection has no trash: a soft delete would leave the
		// bytes served at the URL (ADR-0027).
		return apiutil.MapError(publicread.Rulef(msgPublicHasNoTrash))
	}
	// Only an AVAILABLE object moves to the trash. Asking the state machine
	// anyway matched no row and came back as a version conflict, which told
	// the caller to reload and try again — for an upload that had simply not
	// finished.
	if obj.State != statemachine.StateAvailable {
		return SoftDeleteRefusal(obj.State)
	}
	err := h.sm.SoftDeleteInTx(ctx, objectID, rv, func(ctx context.Context, tx pgx.Tx) error {
		return h.dispatchEventTx(ctx, tx, tenantID, eventtype.ObjectDeleted,
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			objectEventPayload(tenantID, obj.Collection, obj.Key, obj.ObjectID, map[string]any{
				payloadMode: deleteModeSoft,
			}))
	})
	if errors.Is(err, statemachine.ErrConflict) {
		return connect.NewError(connect.CodeAborted, err.Error()).WithCause(err)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	// Best-effort delete-marker write; post-commit, separate concern — a
	// marker hiccup must not undo a delivered delete event.
	LogSideEffect(ctx, EffectDeleteMarker, obj.ObjectID, h.versions.OnSoftDelete(ctx, obj))
	return nil
}

// pendingNotTrashable is the refusal a PENDING object's soft delete gets.
const pendingNotTrashable = "the upload has not completed, so there is nothing to move to the trash; " +
	"the worker discards it some time after its upload URL expires"

// SoftDeleteRefusal says why an object in state cannot move to the trash;
// the batch delete reports the same reason per object. The worker's
// reconciler discards a PENDING row once its upload URL has expired and the
// reconciler's grace period has passed, not the moment the URL expires. A
// PENDING object is an upload that never completed: it holds no bytes to keep.
func SoftDeleteRefusal(state statemachine.State) error {
	if state == statemachine.StatePending {
		return connect.NewError(connect.CodeFailedPrecondition, pendingNotTrashable)
	}
	return connect.Errorf(connect.CodeFailedPrecondition,
		"object state %s cannot move to the trash", state)
}

// MetadataUpdateRefusal says why an object in state takes no metadata or tag
// write: the store matches only an AVAILABLE row, so asking it anyway comes
// back as a version conflict, which reads as worth a retry and never is.
// Batch tag updates report it per object.
func MetadataUpdateRefusal(state statemachine.State) error {
	return connect.Errorf(connect.CodeFailedPrecondition,
		"object state %s takes no metadata update", state)
}

// assertBypassAllowed gates the governance-retention override. It lives on
// the RPC side because it is the only part of the permanent delete that needs
// a principal: BatchDelete's executor runs with no caller on the context, and
// so never bypasses anything.
func (h *Handler) assertBypassAllowed(principal *auth.Principal, bypassGovernance bool) error {
	if !bypassGovernance {
		return nil
	}
	if !mayBypassGovernance(principal) {
		return connect.NewError(connect.CodePermissionDenied, msgBypassNeedsRole)
	}
	return nil
}

// lockRefusal refuses a permanent delete of an object a version lock still
// holds. FindByName already resolved this object, so a lock-state read error
// is a genuine DB fault, not a missing row. It is logged rather than
// silently swallowed — the HardDelete SQL guard still enforces the lock, so
// the delete proceeds and lets it (and the version check) decide; the caller
// may see CodeAborted instead of a precise lock reason, which the log
// explains.
func (h *Handler) lockRefusal(ctx context.Context, tenantID, objectID uuid.UUID, bypassGovernance bool) error {
	locks, err := h.repo.VersionLocks(ctx, tenantID, objectID)
	if err != nil {
		logger.FromContext(ctx).Warn("object lock pre-check failed; relying on SQL guard",
			zap.String("object_id", objectID.String()), zap.Error(err))
		return nil
	}
	if lock, ok := activeLock(locks, time.Now(), bypassGovernance); ok {
		return connect.Errorf(connect.CodeFailedPrecondition,
			"cannot delete: %s", lock.Reason())
	}
	return nil
}

// PermanentDelete removes one object for good: the row and its purge debt and
// the paladin.object.deleted event in ONE transaction, then the storage bytes,
// then the settlement and paladin.object.purged. It takes an object already
// read and already authorized — the caller owns the capability check, the
// Cedar decision and the bypass role — because there are two callers with
// different notions of a caller: the DeleteObject RPC, which has a principal,
// and the BatchDelete executor, which runs asynchronously and has none.
//
// Exported for that second caller. The ordering below is subtle enough that a
// second implementation of it would be a bug waiting to happen, which is the
// whole reason this is a shared method rather than a copied block.
func (h *Handler) PermanentDelete(
	ctx context.Context,
	tenantID uuid.UUID,
	obj Object,
	rv int64,
	bypassGovernance bool,
) error {
	objectID := obj.ObjectID
	collection := obj.Collection
	okPrefix := h.canonicalObjectPrefix(ctx, opDelete, tenantID, collection)

	// Permanent delete. Ordering matters: drop the DB row FIRST, then
	// the storage bytes. The old order (S3 then DB) could delete the
	// bytes and then fail the DB delete, leaving a live row pointing at
	// nothing — and with the object-lock SQL guard it would even delete
	// a locked object's bytes while the row (correctly) survived. DB
	// first means a failed/blocked delete never touches storage; the
	// only residual failure mode is an orphaned object in S3 (a
	// reclaimable cost leak), never a live row with missing bytes.
	backendID, bucket, err := h.deletedBytesAt(ctx, tenantID, obj) // permanent delete (mutation)
	if err != nil {
		return MapResolveErr(err)
	}

	deleteFn := h.repo.HardDeleteTx
	if bypassGovernance {
		deleteFn = h.repo.HardDeleteWithBypassTx
	}

	// Lock pre-check for a clear error. The HardDelete SQL is the
	// non-bypassable safety net (it refuses locked rows even if this
	// check is wrong); this just turns a would-be 0-row "version
	// mismatch" into an accurate FailedPrecondition. Every version's lock
	// counts, since the delete removes every version.
	if err := h.lockRefusal(ctx, tenantID, objectID, bypassGovernance); err != nil {
		return err
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
		PurgeID:     uuid.New(),
		TenantID:    tenantID,
		ObjectID:    obj.ObjectID,
		BackendID:   backendID,
		BucketName:  bucket,
		Collection:  collection,
		Key:         obj.Key,
		ObjectState: obj.State,
	}
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if derr := deleteFn(ctx, tx, tenantID, objectID, rv); derr != nil {
			return derr
		}
		if perr := h.repo.EnqueuePurgeTx(ctx, tx, debt); perr != nil {
			return perr
		}
		return h.dispatchEventTx(ctx, tx, tenantID, eventtype.ObjectDeleted,
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			objectEventPayload(tenantID, obj.Collection, obj.Key, obj.ObjectID, map[string]any{
				payloadMode:             deleteModePermanent,
				payloadBypassGovernance: bypassGovernance,
			}))
	}); err != nil {
		return apiutil.MapError(err)
	}

	h.reclaimBytes(ctx, debt, okPrefix)
	return nil
}

// reclaimBytes is a permanent delete's fast path: reclaim the bytes now, so
// the common case stays synchronous and the debt table stays empty. A failure
// here is not terminal — the debt row survives and worker.PurgeDrainer
// retries it with backoff. Under the path lock: an object uploaded at this
// key since owns the bytes there.
func (h *Handler) reclaimBytes(ctx context.Context, debt PurgeDebt, okPrefix string) {
	tenantID, collection, key := debt.TenantID, debt.Collection, debt.Key
	backendID, bucket := debt.BackendID, debt.BucketName
	var paid bool
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var perr error
		paid, perr = h.repo.PurgeBytesTx(ctx, tx, tenantID, collection, key, func() error {
			return h.storage.DeleteObject(ctx, backendID, bucket, tenantID, collection, key)
		})
		return perr
	}); err != nil {
		logger.FromContext(ctx).Warn("permanent delete: storage delete failed; queued for retry",
			zap.String("tenant_id", tenantID.String()),
			zap.String("collection", collection),
			zap.String("key", key),
			zap.String("bucket", bucket),
			zap.String("purge_id", debt.PurgeID.String()),
			zap.Error(err),
		)
		// Deliberately NOT an error to the caller. The delete IS committed —
		// the row is gone and paladin.object.deleted is enqueued — and the bytes
		// are now owed rather than lost. Returning an error here would tell
		// the client to retry a delete that already succeeded, and its retry
		// would get NotFound. paladin.object.purged is what signals the bytes
		// actually went; it fires from the drainer instead of here.
		return
	}
	if !paid {
		// A new object is being written at this key; the drainer settles the
		// debt once it can tell whose bytes are there.
		return
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
		return h.dispatchEventTx(ctx, tx, tenantID, eventtype.ObjectPurged,
			objectResourceNameFrom(okPrefix, tenantID, collection, key),
			objectEventPayload(tenantID, collection, key, debt.ObjectID, map[string]any{
				payloadBackendID: backendID,
				payloadBucket:    bucket,
				payloadReclaimed: true,
			}))
	}); err != nil {
		// The bytes are gone but the bookkeeping did not commit. Harmless and
		// self-correcting: the drainer will retry a DELETE against a key that
		// no longer exists (S3 DELETE is idempotent), succeed, and emit the
		// event then.
		logger.FromContext(ctx).Warn("permanent delete: bytes reclaimed but purge bookkeeping failed; drainer will settle",
			zap.String("purge_id", debt.PurgeID.String()),
			zap.Error(err),
		)
	}
}

// ─── RestoreObject ──────────────────────────────────────────────────────────

// RestoreObject brings a soft-deleted object back. resourceVersion enforces
// OCC against the row read here and is mandatory; an empty one is rejected
// rather than skipping the check.
// Versioning-aware: when bucket has versioning_enabled, also drops the most
// recent delete-marker before flipping state.
func (h *Handler) RestoreObject(ctx context.Context, collection, objectIDStr, resourceVersion string) (_ *Object, err error) {
	defer func() {
		logOutcome(ctx, h.log, opRestore, zapcore.InfoLevel, err, objectFields(collection, objectIDStr)...)
	}()
	// Shape of the request first — no identity, no lookup needed. Same order
	// as VersionHandler.RestoreVersion, and it means the refusal cannot be
	// mistaken for an authentication failure.
	expected, err := requiredResourceVersion(resourceVersion)
	if err != nil {
		return nil, err
	}
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	objectID, err := parseObjectName(collection, objectIDStr)
	if err != nil {
		return nil, err
	}
	obj, err := h.repo.FindByName(ctx, tenantID, collection, objectIDStr)
	if err != nil {
		return nil, objectLookupError(err)
	}
	objectURI := paladin.ObjectResource(tenantID.String(), obj.Collection, obj.Key)
	// Restore is conceptually a Put (re-creates the live object from a
	// soft-deleted row). Capability gate on OpPut.
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return nil, err
	}
	// A restore takes the row out of the trash; its bytes never left the
	// bucket (assertBytesPresent reads them), so it is a metadata write
	// through the write gate, and Cedar decides on the binding it resolves.
	rsBackendID, rsBucket, err := writeBinding(ctx, h.repo, tenantID, obj.Collection, bucketgate.MetadataWrite)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: obj.Collection, Key: obj.Key,
		BackendID: rsBackendID, BucketName: rsBucket,
		ContentType: obj.ContentType, SizeBytes: obj.SizeBytes, Tags: obj.Tags,
	}, cedar.ActionRestoreObject, obj.SizeBytes, obj.ContentType); err != nil {
		return nil, err
	}
	if err := h.checkRestorable(ctx, tenantID, collection, obj, expected); err != nil {
		return nil, err
	}
	return h.restoreTrashed(ctx, tenantID, collection, objectID, obj)
}

// requiredResourceVersion parses a restore's resource_version, which is
// mandatory: an empty one is refused rather than skipping the check.
func requiredResourceVersion(resourceVersion string) (int64, error) {
	if resourceVersion == "" {
		return 0, connect.NewError(connect.CodeInvalidArgument,
			"resource_version is required")
	}
	expected, err := parseInt64(resourceVersion)
	if err != nil {
		return 0, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	return expected, nil
}

// checkRestorable refuses to restore an object that is not in the trash, has
// moved on since the caller read it, would land on a live object, or has no
// bytes left to serve.
func (h *Handler) checkRestorable(ctx context.Context, tenantID uuid.UUID, collection string, obj Object, expected int64) error {
	if obj.State != statemachine.StateDeleted {
		return connect.Errorf(connect.CodeFailedPrecondition,
			"cannot restore from state %s", obj.State)
	}
	// OCC against the row just loaded. The guard is mandatory (validated at
	// the top); the old form skipped the check on an empty string, arguing a
	// concurrent mutation of a DELETED row was vanishingly rare. "Rare" is
	// not "impossible", and HardDelete is the other mutation path on that
	// row — restoring over one is exactly the race worth refusing.
	if expected != obj.ResourceVersion {
		return connect.Errorf(connect.CodeAborted,
			"resource_version mismatch: expected %d, current %d", expected, obj.ResourceVersion)
	}
	collision, err := h.repo.LiveCollision(ctx, tenantID, collection, obj.Key)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if collision {
		return connect.Errorf(connect.CodeFailedPrecondition,
			"a live object already occupies %s/%s", collection, obj.Key)
	}
	return h.assertBytesPresent(ctx, tenantID, obj)
}

// restoreTrashed takes a restorable object out of the trash. Versioning-aware:
// if the parent bucket has versioning_enabled AND the most recent version is a
// delete-marker, the pointer drops back to the previous non-marker version.
// This makes RestoreObject a single "make visible again" affordance whether or
// not versioning is on.
func (h *Handler) restoreTrashed(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID, obj Object) (*Object, error) {
	if err := h.versions.UnsetDeleteMarkerCurrent(ctx, obj); err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("restore version pointer: %w", err))
	}
	// Restore + paladin.object.restored fan-out in one tx (ADR-0003). Resource
	// identity (collection/key/object_id) is unchanged by restore, so the
	// payload is built from the pre-restore `obj`.
	okPrefix := h.canonicalObjectPrefix(ctx, opRestore, tenantID, collection)
	if err := h.sm.RestoreInTx(ctx, objectID, func(ctx context.Context, tx pgx.Tx) error {
		return h.dispatchEventTx(ctx, tx, tenantID, eventtype.ObjectRestored,
			objectResourceNameFrom(okPrefix, tenantID, obj.Collection, obj.Key),
			objectEventPayload(tenantID, obj.Collection, obj.Key, obj.ObjectID, nil))
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, collection, objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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

func (h *Handler) CopyObject(ctx context.Context, in CopyObjectInput) (out *Object, err error) {
	defer func() {
		fields := append(objectFields(in.SourceCollection, in.SourceObjectID), zap.String("dest_collection", in.DestCollection))
		if out != nil {
			fields = append(fields, zap.String("dest_object_id", out.ObjectID.String()))
		}
		logOutcome(ctx, h.log, opCopy, zapcore.InfoLevel, err, fields...)
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkCopyInput(in); err != nil {
		return nil, err
	}
	src, err := h.copySource(ctx, tenantID, in)
	if err != nil {
		return nil, err
	}
	dstMeta, destKey, err := h.copyDestination(ctx, tenantID, in, src)
	if err != nil {
		return nil, err
	}
	// Copy = Put on the destination URI (semantically a write of new
	// content) — gated by OpPut. The source must already be readable
	// to the caller; we don't separately gate OpGet on it because the
	// underlying access model treats source-readable-and-dest-writable
	// as the union of the same Cedar policy below.
	destURI := paladin.ObjectResource(tenantID.String(), in.DestCollection, destKey)
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, destURI); err != nil {
		return nil, err
	}
	// The copy creates an object in the destination bucket, so it obeys that
	// bucket's limits like an upload would; otherwise a copy is the way
	// around an allowlist or a size cap.
	if err := uploadpolicy.For(h.presign.Limits, dstMeta.Constraints).CheckCopy(uploadpolicy.Upload{
		SizeBytes: src.SizeBytes, ContentType: src.ContentType, ChecksumAlgorithm: src.ChecksumAlgo,
	}); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.DestCollection, Key: destKey,
		BackendID: dstMeta.BackendID, BucketName: dstMeta.BucketName,
		ContentType: src.ContentType, SizeBytes: src.SizeBytes, Tags: in.Tags,
	}, cedar.ActionCopyObject, src.SizeBytes, src.ContentType); err != nil {
		return nil, err
	}
	return h.copyBytes(ctx, tenantID, in, src, dstMeta, destKey)
}

// checkCopyInput refuses a copy that does not name its source object and
// its destination collection.
func checkCopyInput(in CopyObjectInput) error {
	if in.SourceCollection == "" || in.SourceObjectID == "" {
		return connect.NewError(connect.CodeInvalidArgument,
			"source_collection and source_object_id are required")
	}
	if in.DestCollection == "" {
		return connect.NewError(connect.CodeInvalidArgument,
			"dest_collection is required")
	}
	return AdmitOptionalKey(FieldDestinationKey, in.DestKey)
}

// copySource reads the object a copy is of; only an AVAILABLE one has bytes
// to copy.
func (h *Handler) copySource(ctx context.Context, tenantID uuid.UUID, in CopyObjectInput) (Object, error) {
	src, err := h.repo.FindByName(ctx, tenantID, in.SourceCollection, in.SourceObjectID)
	if err != nil {
		return Object{}, objectLookupError(err)
	}
	if src.State != statemachine.StateAvailable {
		return Object{}, connect.Errorf(connect.CodeFailedPrecondition,
			"cannot copy from state %s", src.State)
	}
	return src, nil
}

// copyDestination resolves where a copy lands, BEFORE the Cedar check, so a
// bucket:/collection:-scoped write PAT enforces on the copy target — the
// authz Resource is the destination (ActionCopyObject is checked against the
// dest). The same resolution is reused as the copy-dest Location; the source
// is resolved after authz. A public destination names the copy itself;
// elsewhere an empty key keeps the source's.
func (h *Handler) copyDestination(ctx context.Context, tenantID uuid.UUID, in CopyObjectInput, src Object) (BucketMeta, string, error) {
	dstMeta, err := h.repo.LookupBucketMeta(ctx, tenantID, in.DestCollection, bucketgate.BytesWrite) // copy dest (mutation)
	if err != nil {
		return BucketMeta{}, "", MapResolveErr(err)
	}
	destKey := in.DestKey
	if err := AdmitPublicUpload(dstMeta, &destKey, src.ContentType); err != nil {
		return BucketMeta{}, "", err
	}
	if destKey == "" {
		// The source's key, which may predate the key contract.
		destKey = src.Key
		if err := AdmitKey(FieldDestinationKey, destKey); err != nil {
			return BucketMeta{}, "", err
		}
	}
	return dstMeta, destKey, nil
}

// copyBytes carries out an authorized copy: the destination row, the
// storage copy, and the promotion with its event.
func (h *Handler) copyBytes(ctx context.Context, tenantID uuid.UUID, in CopyObjectInput, src Object, dstMeta BucketMeta, destKey string) (*Object, error) {
	dstBackendID, dstBucket := dstMeta.BackendID, dstMeta.BucketName
	from, err := StoredAt(src, bucketgate.Read) // copy source (read), where its bytes are
	if err != nil {
		return nil, MapResolveErr(err)
	}
	publicURL, err := h.publicURL(ctx, dstMeta, tenantID, in.DestCollection, destKey)
	if err != nil {
		return nil, err
	}
	// Insert the destination row up-front so the FK to collections is
	// validated before we issue the S3 copy.
	dst, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID:         tenantID,
		Collection:       in.DestCollection,
		Key:              destKey,
		ContentType:      src.ContentType,
		SizeBytes:        &src.SizeBytes,
		ChecksumAlgo:     src.ChecksumAlgo,
		Metadata:         coalesceMap(in.Metadata, src.Metadata),
		Tags:             coalesceMap(in.Tags, src.Tags),
		ExternalRef:      src.ExternalRef,
		PresignExpiresAt: time.Now().Add(h.presign.TTL.Default(presignttl.OpPut)),
		PublicURL:        publicURL,
	})
	if err != nil {
		return nil, mapCreateErr(err)
	}
	if err := h.storage.CopyObject(ctx, from, Location{
		BackendID: dstBackendID, TenantID: tenantID, Bucket: dstBucket, Collection: in.DestCollection, Key: destKey,
		CacheControl: dstMeta.CacheControl, ContentType: src.ContentType,
	}); err != nil {
		return nil, h.copyFailed(ctx, dst.ObjectID, err)
	}
	// Promote + paladin.object.uploaded fan-out in one tx (ADR-0003): the
	// copy materialises a brand-new object, so subscribers see the same
	// `paladin.object.uploaded` they'd get from a normal CompleteObject path.
	// The payload's `source` discriminator lets a consumer that cares
	// about origin route copies vs direct uploads.
	changed, err := h.promoteUploaded(ctx, opCopy, dst.ObjectID, Location{
		BackendID: dstBackendID, TenantID: tenantID, Bucket: dstBucket, Collection: in.DestCollection, Key: destKey,
	}, storedBytes{etag: src.ETag, size: src.SizeBytes, checksum: src.Checksum}, map[string]any{
		payloadSizeBytes:        src.SizeBytes,
		payloadETag:             src.ETag,
		payloadContentType:      src.ContentType,
		payloadSource:           uploadSourceCopy,
		payloadSourceCollection: in.SourceCollection,
		payloadSourceObjectID:   in.SourceObjectID,
	})
	if err != nil {
		return nil, err
	}
	// Same tail as CompleteObject. The capability charge belongs here too — a
	// copy is gated by OpPut and materialises a new stored object, so a
	// budgeted principal that could copy for free would be able to consume
	// storage without spending anything.
	return h.promotedObject(ctx, tenantID, in.DestCollection, dst.ObjectID.String(), changed)
}

// copyFailed compensates a storage copy that failed: the destination row was
// created PENDING, and without this transition it would linger forever,
// since the reconciler only promotes via HEAD against an object that the
// failed copy never wrote.
func (h *Handler) copyFailed(ctx context.Context, dstID uuid.UUID, err error) error {
	if mfErr := h.sm.DiscardFailed(ctx, dstID, statemachine.FailedStorageCopy); mfErr != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("storage copy: %w (compensation also failed: %w)", err, mfErr))
	}
	return rpcerr.New(connect.CodeInternal, fmt.Errorf("storage copy: %w", err))
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
	LogSideEffect(ctx, EffectQuota, obj.ObjectID, h.quota.OnObjectPromoted(ctx, obj.TenantID, obj.ObjectID, obj.SizeBytes))
}

func (h *Handler) authorize(
	ctx context.Context,
	principal *auth.Principal,
	tenantID uuid.UUID,
	res *cedar.Resource,
	action cedar.Action,
	sizeBytes int64,
	contentType string,
) error {
	return apiutil.Decide(ctx, h.policy, apiutil.CedarPrincipal(principal), action, res,
		cedar.RequestContext{SizeBytes: sizeBytes, ContentType: contentType})
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

// ErrPathTaken is what a caller is told of an object created at a path
// another object holds, in any state, the trash included.
var ErrPathTaken = errors.New("an object already exists at this key in the collection")

// PathTaken answers err, the unique index refusing a new object's path, as
// AlreadyExists in the product's words; the index and its SQLSTATE stay in
// the cause, for the log.
func PathTaken(err error) error {
	return connect.NewError(connect.CodeAlreadyExists, ErrPathTaken.Error()).WithCause(errors.Join(ErrPathTaken, err))
}

func mapCreateErr(err error) error {
	if err == nil {
		return nil
	}
	if pgerr.Is(err, pgerr.UniqueViolation) {
		return PathTaken(err)
	}
	return apiutil.MapError(err)
}

// ErrNotUploaded is what a caller completing an upload is told when nothing
// is at the key.
var ErrNotUploaded = errors.New("object not uploaded yet")

// headFailure answers a HEAD that failed while completing an upload: nothing
// at the key is the caller's to fix by uploading; a backend that could not be
// asked is unavailable, not a refusal. The backend's own answer stays in the
// cause: its request ids and status line are nothing the caller can act on.
// An error that already carries its code — a bucket gate's refusal, a failed
// move of the upload between buckets — keeps it.
func headFailure(err error) error {
	if _, ok := errors.AsType[*connect.Error](err); ok {
		return err
	}
	if errors.Is(err, ErrStoredObjectNotFound) {
		return connect.NewError(connect.CodeFailedPrecondition, ErrNotUploaded.Error()).WithCause(errors.Join(ErrNotUploaded, err))
	}
	return connect.NewError(connect.CodeUnavailable, msgUploadUnchecked).WithCause(fmt.Errorf("check the uploaded bytes: %w", err))
}

// What a caller is told when the backend could not be asked about an
// object's bytes. The backend's answer names its URL and request id; it stays
// the cause, which the RPC log line carries.
const (
	msgUploadUnchecked = "the uploaded bytes could not be checked; retry"
	// msgBindingMoved: a storage migration moved the collection to another
	// bucket while the object was written.
	msgBindingMoved   = "the collection moved to another bucket while the object was written; retry"
	msgBytesUnchecked = "the object's bytes could not be checked; retry"
)

// assertBytesPresent HEADs a trashed object before it is restored. Its bytes
// may have been deleted straight from the bucket, which moves it to the trash
// through a storage event; a HEAD that cannot reach the backend is
// unavailable, not a refusal.
func (h *Handler) assertBytesPresent(ctx context.Context, tenantID uuid.UUID, obj Object) error {
	at, err := StoredAt(obj, bucketgate.Read) // a HEAD; RestoreObject passed the write gate already
	if err != nil {
		return MapResolveErr(err)
	}
	_, _, _, _, err = h.storage.Head(ctx, at.BackendID, at.Bucket, tenantID, obj.Collection, obj.Key, obj.ChecksumAlgo)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrStoredObjectNotFound):
		return apiutil.MapError(ErrObjectBytesMissing)
	default:
		return connect.NewError(connect.CodeUnavailable, msgBytesUnchecked).WithCause(fmt.Errorf("check the object's bytes: %w", err))
	}
}

// ErrVersionMismatch is returned by Repository implementations when an
// optimistic-concurrency update fails (resource_version did not match).
var ErrVersionMismatch = errors.New("resource_version mismatch")

// Register this package's sentinels with the central error→Connect-code
// mapper so callers can route through apiutil.MapError for a consistent
// code instead of a hand-written per-handler if/else. errors.Is-based
// matching, so the existing local checks keep working unchanged.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT)
	apiutil.RegisterError(ErrVersionNotFound, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_OBJECT_VERSION_NOT_FOUND)
	apiutil.RegisterError(ErrObjectBytesMissing, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_OBJECT_BYTES_MISSING)
	for _, r := range bucketgate.Refusals() {
		apiutil.RegisterError(r, r.Code, r.Reason)
	}
}

// The side effects of an object's transition that are written after it
// commits, as LogSideEffect names them.
const (
	EffectVersion        = "version history"
	EffectDeleteMarker   = "delete marker"
	EffectQuota          = "quota usage"
	EffectStorageCleanup = "storage cleanup"
)

// LogSideEffect logs a side effect of an object's transition that failed
// after the transition committed. The transition stands — undoing a
// delivered event for a version row would be worse — but a version missing
// from the history, or quota left drifting until the reconciler, was
// otherwise invisible.
func LogSideEffect(ctx context.Context, effect string, objectID uuid.UUID, err error) {
	if err == nil {
		return
	}
	logger.FromContext(ctx).Warn("object side effect not written",
		zap.String("effect", effect),
		zap.String("object_id", objectID.String()),
		zap.Error(err))
}
