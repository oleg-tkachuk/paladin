// Package multipart implements the MultipartService business logic.
//
// Lifecycle: Initiate → (PresignPart × N) → Complete|Abort. Session state
// lives in multipart_uploads; the parts themselves are known only to the
// storage backend, because clients PUT them there directly through presigned
// URLs and no part upload passes through Paladin.
package multiparth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Storage interface {
	// InitiateMultipart opens the storage upload with the checksum algorithm
	// its parts are bound by.
	// cacheControl is bound into the object in a public collection, "" elsewhere.
	InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType, cacheControl, checksumAlgo string) (storageUploadID string, err error)
	// PublicURL is objecth.Storage's: where a public object is read unsigned.
	PublicURL(ctx context.Context, backendID, bucket, publicBaseURL string, tenantID uuid.UUID, collection, key string) (string, error)
	// CompleteMultipart assembles the parts; each carries the checksum it was
	// presigned with, which the store checks against the part it recorded.
	CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key, checksumAlgo string, parts []PartETag) (etag string, sizeBytes int64, err error)
	AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string) error
	// ListMultipartParts asks the backend which parts have actually landed.
	// It has to be the backend: clients PUT parts straight to the object
	// store through presigned URLs, so no part upload passes through Paladin
	// and no table here can know what arrived.
	ListMultipartParts(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, maxParts, afterPartNumber int32) ([]Part, int32, error)
	PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, part PartBinding, ttl time.Duration) (url string, headers map[string]string, expiresAt time.Time, err error)
	// DeleteObject removes an assembled object whose bytes broke its
	// registration (see discardMismatched).
	DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error
	// CopyObject and Head move an object assembled in the bucket its session
	// was anchored to into the one its collection is bound to now, and check
	// the copy (relocate).
	CopyObject(ctx context.Context, src, dst objecth.Location) error
	Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, checksumAlgo string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
}

// StateMachine is the slice of *statemachine.Transitioner the handler uses.
type StateMachine interface {
	PromoteToAvailable(ctx context.Context, objectID uuid.UUID, placed *statemachine.Placement, etag string, sizeBytes int64, checksum, sequencer string, source statemachine.Source) (bool, error)
	DiscardFailed(ctx context.Context, objectID uuid.UUID, reason statemachine.FailureReason) error
}

// PartBinding is what one part URL is signed for: the part's number, its
// exact length and its checksum.
type PartBinding struct {
	Number        int32
	SizeBytes     int64
	ChecksumAlgo  string
	ChecksumValue string
}

// Part is one part the storage backend reports as uploaded.
type Part struct {
	PartNumber int32
	SizeBytes  int64
	ETag       string
	Checksum   string
	UploadedAt time.Time
}

type PartETag struct {
	PartNumber    int32
	ETag          string
	ChecksumValue string
}

// SessionRef is the object a caller claims an upload session belongs to,
// parsed from the request's object_name. Every per-session RPC carries that
// name, and until this existed none of them checked it: the session was found
// by upload_id alone and the name was accepted unread. A mismatched pair —
// wrong name, right id — was answered as though it were right.
type SessionRef struct {
	Collection string
	ObjectID   uuid.UUID
}

// assertSessionMatches rejects a request whose object_name disagrees with the
// session it names. InvalidArgument rather than NotFound: the tenant check
// above has already established the caller may see this session, so the
// disagreement is the caller's bookkeeping, not a probe — and saying so is
// more useful than pretending the session is missing.
func assertSessionMatches(sess Session, want SessionRef) error {
	if want.ObjectID == uuid.Nil() && want.Collection == "" {
		return nil // caller did not name an object (internal call sites)
	}
	if want.ObjectID != uuid.Nil() && sess.ObjectID != want.ObjectID {
		return connect.Errorf(connect.CodeInvalidArgument,
			"object_name names object %s but upload %s belongs to %s",
			want.ObjectID, sess.UploadID, sess.ObjectID)
	}
	if want.Collection != "" && sess.Collection != want.Collection {
		return connect.Errorf(connect.CodeInvalidArgument,
			"object_name names collection %q but upload %s belongs to %q",
			want.Collection, sess.UploadID, sess.Collection)
	}
	return nil
}

type Session struct {
	UploadID        string
	ObjectID        uuid.UUID
	TenantID        uuid.UUID
	BackendID       string // storage backend the upload was initiated against
	Bucket          string // physical S3 bucket; anchored at initiate time
	Collection      string
	Key             string
	StorageUploadID string
	PartSizeBytes   int64
	TotalParts      int32
	// SizeBytes is the object's registered size; with PartSizeBytes it fixes
	// every part's exact length. ChecksumAlgo is the algorithm every part is
	// checksummed with.
	SizeBytes    int64
	ChecksumAlgo string
	CreatedAt    time.Time
	// BucketState is the anchored bucket's state as the bucket gate decides
	// on it: its backend's flags and its own provision state. Every RPC on
	// the session asks the gate about it (target), never about the
	// collection's current binding, which a storage migration may have
	// moved since initiate.
	BucketState bucketgate.State
	// BucketConstraints are the anchored bucket's upload constraints, which
	// bound a part URL's lifetime.
	BucketConstraints uploadpolicy.BucketConstraints
}

// PartLength is part n's exact length: the planned part size, except the
// last part, which carries the remainder.
func (s Session) PartLength(n int32) int64 {
	if n < s.TotalParts {
		return s.PartSizeBytes
	}
	return s.SizeBytes - int64(s.TotalParts-1)*s.PartSizeBytes
}

type InitiateArgs struct {
	TenantID uuid.UUID
	// InitiatedBy* attribute the upload to the principal that started it.
	// Filled from the request context, never by the caller — see Initiate.
	InitiatedBySubject string
	InitiatedByKind    string
	Collection         string
	Key                string
	ContentType        string
	TotalParts         int32
	PartSizeBytes      int64
	SizeHint           int64
	ChecksumAlgo       string
	Metadata           map[string]string
	Tags               map[string]string
	// ExternalRef is the caller's own identifier for the object, stored on
	// the objects row. Accepted by InitiateMultipartUpload and dropped on the
	// floor until now: the field existed on the request, on this struct's
	// single-shot sibling, and as a column — just not on the path between
	// them.
	ExternalRef string
	// PublicURL is the object's address in a public collection (ADR-0027);
	// "" elsewhere. Set by Initiate, never by the caller.
	PublicURL string
}

type CompleteArgs struct {
	TenantID uuid.UUID
	UploadID string
	Parts    []PartETag
}

type Repository interface {
	InitiateSession(ctx context.Context, args InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (Session, error)
	// GetSession reads a session with the bucket it was anchored to at
	// initiate, that bucket's gate state and its constraints.
	GetSession(ctx context.Context, uploadID string) (Session, error)
	DeleteSession(ctx context.Context, uploadID string) error
	// LookupBucketMeta resolves the collection's current binding through the
	// bucket gate, with the bucket's upload constraints, for initiate, which
	// anchors the session to it.
	LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, access bucketgate.Access) (objecth.BucketMeta, error)
	// Object reads the object a completed upload produced, to answer
	// CompleteMultipartUpload with it as CompleteObject answers with its own.
	Object(ctx context.Context, tenantID, objectID uuid.UUID) (objecth.Object, error)
	// RecordCompositeChecksum records a PENDING object's composite checksum
	// and its part size, before the store assembles it.
	RecordCompositeChecksum(ctx context.Context, objectID uuid.UUID, composite string, partSizeBytes int64) error
}

// VersionRecorder is the optional hook that records a versions-row when the
// parent bucket has versioning_enabled. Implemented by *object.VersionHandler
// (declared as an interface here to avoid a cycle).
type VersionRecorder interface {
	OnPromote(ctx context.Context, obj VersionedObject) error
}

// VersionedObject is the minimal projection the recorder needs at promote
// time. Mirrors object.Object so the wiring layer can pass the same value
// through both packages without a separate conversion step.
type VersionedObject struct {
	ObjectID     uuid.UUID
	TenantID     uuid.UUID
	Collection   string
	Key          string
	ContentType  string
	SizeBytes    int64
	ETag         string
	ChecksumAlgo string
	Checksum     string
	// ChecksumPartSizeBytes is the part size Checksum is a composite over.
	ChecksumPartSizeBytes int64
	Metadata              map[string]string
	Tags                  map[string]string
}

// QuotaUpdater is the post-promote accounting hook — same shape as the
// one on object.Handler. Multipart and single-PUT promotions share usage
// counters per tenant.
type QuotaUpdater interface {
	OnObjectPromoted(ctx context.Context, tenantID, objectID uuid.UUID, sizeBytes int64) error
}

// ErrUploadNotFound is the repository's answer for an upload session the
// tenant does not have — an id that does not parse names none either. Any
// other GetSession error is the store failing.
var ErrUploadNotFound = errors.New("upload not found")

// ErrNotPending is RecordCompositeChecksum's answer when the upload's object
// is no longer PENDING, so nothing was recorded: a storage event or an
// earlier completion promoted it, or it has moved on since.
var ErrNotPending = errors.New("upload's object is no longer pending")

// The RPCs as the outcome log names them.
const (
	opInitiate    = "initiate"
	opComplete    = "complete"
	opAbort       = "abort"
	opPresignPart = "presign part"
	opListParts   = "list parts"
)

type Handler struct {
	repo     Repository
	storage  Storage
	policy   cedar.Authorizer
	sm       StateMachine
	ttl      presignttl.Policy
	limits   uploadpolicy.Limits
	versions VersionRecorder // optional
	quota    QuotaUpdater    // optional
	log      *zap.Logger
}

// SetLogger sets where each RPC says how it ended. Unset, nothing is logged.
func (h *Handler) SetLogger(l *zap.Logger) { h.log = l }

// SetQuotaUpdater attaches the optional usage hook called on
// CompleteMultipartUpload after a successful promotion. nil = no-op.
func (h *Handler) SetQuotaUpdater(q QuotaUpdater) { h.quota = q }

func NewHandler(repo Repository, storage Storage, policy cedar.Authorizer, sm StateMachine, ttl presignttl.Policy, limits uploadpolicy.Limits) *Handler {
	return &Handler{repo: repo, storage: storage, policy: policy, sm: sm, ttl: ttl, limits: limits}
}

// SetVersionRecorder attaches the optional recorder used after a successful
// CompleteMultipartUpload promote. Wired by main.
func (h *Handler) SetVersionRecorder(v VersionRecorder) { h.versions = v }

// InitiateMultipartUpload creates the PENDING object row and opens a storage
// multipart session. Handler contract: size_bytes is required here because
// part sizing needs it (unlike UploadObject where it's a hint).
func (h *Handler) InitiateMultipartUpload(ctx context.Context, args InitiateArgs) (out *Session, err error) {
	defer func() {
		fields := []zap.Field{zap.String("collection", args.Collection)}
		if out != nil {
			fields = append(fields, zap.String("upload_id", out.UploadID), zap.String("object_id", out.ObjectID.String()))
		}
		h.logOutcome(ctx, opInitiate, zapcore.InfoLevel, err, fields...)
	}()
	tenantID, p, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := objecth.AdmitOptionalKey(objecth.FieldKey, args.Key); err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	// Attribution comes from the caller's principal, never from the request:
	// a client must not be able to name someone else as the initiator.
	args.InitiatedBySubject = p.Subject
	args.InitiatedByKind = p.Kind.String()
	// The client does not choose the part size — the request has no field
	// for it, and the response promises a `recommended_part_size` plus a
	// `total_parts`. Computing them here is what makes those promises true:
	// they were left zero, so a caller had nothing to slice the file by and
	// PresignPart rejected every part number as out of range.
	// Resolve the (backend, bucket) BEFORE authz so a bucket:/collection:-
	// scoped write PAT enforces on multipart init; the same resolution routes
	// InitiateMultipart and anchors the session below. Before the capability
	// check too: a public collection names the object itself, and the check
	// is of the object it will create.
	meta, err := h.repo.LookupBucketMeta(ctx, tenantID, args.Collection, bucketgate.BytesWrite) // multipart init (mutation)
	if err != nil {
		return nil, objecth.MapResolveErr(err)
	}
	backendID, bucket := meta.BackendID, meta.BucketName
	if err := objecth.AdmitPublicUpload(meta, &args.Key, args.ContentType); err != nil {
		return nil, err
	}
	// A key left empty is named by the server, as an upload's is.
	args.Key = objecth.UploadKey(args.Key)
	objectURI := paladin.ObjectResource(tenantID.String(), args.Collection, args.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return nil, err
	}
	// The plan honours limits.* narrowed by the bucket: max_multipart_size,
	// the part-size bounds and max_parts, plus the type and checksum rules
	// every upload obeys. A size it cannot plan is refused here, not at the
	// part that would not fit.
	plan, err := uploadpolicy.For(h.limits, meta.Constraints).PlanMultipart(uploadpolicy.Upload{
		SizeBytes: args.SizeHint, ContentType: args.ContentType, ChecksumAlgorithm: args.ChecksumAlgo,
	})
	if err != nil {
		return nil, err
	}
	args.PartSizeBytes, args.TotalParts = plan.PartSize, plan.TotalParts
	if err := h.authorize(ctx, p, tenantID, args.Collection, args.Key, backendID, bucket, cedar.ActionPutObject, args.SizeHint, args.ContentType); err != nil {
		return nil, err
	}
	return h.openSession(ctx, args, meta)
}

// openSession opens the admitted upload args describes: the storage upload
// first, then the PENDING object and the session that tracks it.
func (h *Handler) openSession(ctx context.Context, args InitiateArgs, meta objecth.BucketMeta) (*Session, error) {
	tenantID, backendID, bucket := args.TenantID, meta.BackendID, meta.BucketName
	if meta.PublicRead {
		var err error
		args.PublicURL, err = h.storage.PublicURL(ctx, backendID, bucket, meta.PublicBaseURL, tenantID, args.Collection, args.Key)
		if err != nil {
			return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("public url: %w", err))
		}
	}
	storageUploadID, err := h.storage.InitiateMultipart(ctx, backendID, bucket, tenantID, args.Collection, args.Key, args.ContentType, meta.CacheControl, args.ChecksumAlgo)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("storage initiate: %w", err))
	}

	objectID := uuid.NewV7()
	session, err := h.repo.InitiateSession(ctx, args, objectID, storageUploadID, backendID, bucket)
	if err != nil {
		// Best-effort rollback: abort the orphan storage session. Log and
		// proceed — a background sweeper eventually cleans stragglers.
		objecth.LogSideEffect(ctx, objecth.EffectStorageCleanup, objectID,
			h.storage.AbortMultipart(ctx, backendID, bucket, tenantID, storageUploadID, args.Collection, args.Key))
		// The path is taken — by an object in any state, the trash included —
		// as UploadObject reports it: a conflict the caller can act on, not a
		// failure of the server.
		if pgerr.Is(err, pgerr.UniqueViolation) {
			return nil, objecth.PathTaken(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &session, nil
}

// CompleteMultipartUpload assembles the parts and promotes the object, and
// returns it as stored. A read of it that fails after the completion
// succeeded returns a zero Object rather than an error: the upload is done,
// and a caller told otherwise would retry a session that no longer exists.
func (h *Handler) CompleteMultipartUpload(ctx context.Context, args CompleteArgs) (_ objecth.Object, err error) {
	defer func() { h.logOutcome(ctx, opComplete, zapcore.InfoLevel, err, zap.String("upload_id", args.UploadID)) }()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return objecth.Object{}, err
	}
	args.TenantID = tenantID

	sess, err := h.session(ctx, tenantID, args.UploadID, SessionRef{})
	if err != nil {
		return objecth.Object{}, err
	}
	objectURI := paladin.ObjectResource(tenantID.String(), sess.Collection, sess.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return objecth.Object{}, err
	}
	// Through the write gate before Cedar, then authorized on where the parts
	// live, so a bucket:/collection:-scoped PAT enforces on complete.
	backendID, bucket, err := h.target(sess, bucketgate.BytesWrite)
	if err != nil {
		return objecth.Object{}, err
	}
	if err := h.authorize(ctx, principal, tenantID, sess.Collection, sess.Key, backendID, bucket, cedar.ActionPutObject, 0, ""); err != nil {
		return objecth.Object{}, err
	}
	composite, err := h.recordComposite(ctx, sess, args.Parts)
	if err != nil {
		return objecth.Object{}, err
	}
	a, err := h.assemble(ctx, sess, backendID, bucket, args)
	if err != nil {
		return objecth.Object{}, err
	}
	h.afterPromote(ctx, sess, a, composite)
	// Non-fatal — the object is AVAILABLE and the TTL sweep will collect the
	// row. Logged because "the sweep will get it" is an assumption: if this
	// fails for every upload, sessions accumulate until the sweep is the only
	// thing keeping the table finite, and nothing else would say so.
	h.dropSession(ctx, args.UploadID, "multipart session not deleted after complete")
	return h.completedObject(ctx, tenantID, sess.ObjectID, args.UploadID), nil
}

// errUnanchored is a session that names no bucket. multipart_uploads.bucket_id
// is NOT NULL, so only a repository breaking its contract answers one, and no
// operation is authorized on an empty binding.
var errUnanchored = errors.New("upload session names no bucket")

// target passes the bucket sess was anchored to at initiate — where its parts
// live — through the bucket gate for access, and answers it, for Cedar and
// for routing. The gate decides on that bucket's own state
// (Session.BucketState), not on the collection's current binding: after a
// storage migration rebinds the collection, a session anchored to the old,
// draining backend still writes there, and is refused there.
//
// Completing an upload and signing a part URL write bytes (BytesWrite).
// Aborting one frees storage and listing its parts reads it (Read): both are
// refused only on a disabled backend, as the abort workers are
// (internal/worker/bucket_gate.go).
func (h *Handler) target(sess Session, access bucketgate.Access) (backendID, bucket string, err error) {
	if sess.BackendID == "" || sess.Bucket == "" {
		return "", "", rpcerr.New(connect.CodeInternal, fmt.Errorf("upload %s: %w", sess.UploadID, errUnanchored))
	}
	if err := bucketgate.Check(access, sess.BucketState); err != nil {
		return "", "", objecth.MapResolveErr(err)
	}
	return sess.BackendID, sess.Bucket, nil
}

// recordComposite checks the completion list and records the composite of
// the parts' checksums, which the store verified each part against: what a
// reader recomputes from the bytes. Recorded before the store assembles the
// object, so it is in place whichever promotes the row first — this call or
// the storage event.
func (h *Handler) recordComposite(ctx context.Context, sess Session, parts []PartETag) (string, error) {
	if err := sess.checkParts(parts); err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	composite, err := sess.compositeChecksum(parts)
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	err = h.repo.RecordCompositeChecksum(ctx, sess.ObjectID, composite, sess.PartSizeBytes)
	if errors.Is(err, ErrNotPending) {
		return composite, h.notPending(ctx, sess, composite)
	}
	if err != nil {
		return "", rpcerr.New(connect.CodeInternal, fmt.Errorf("record composite checksum: %w", err))
	}
	return composite, nil
}

// notPending decides a completion whose object recorded no composite because
// it is past PENDING. The same completion retried after its object was
// promoted — AVAILABLE, holding this composite — goes on, and the promote
// that follows is a no-op. Anything else is refused before the store
// assembles parts no PENDING object is waiting for.
func (h *Handler) notPending(ctx context.Context, sess Session, composite string) error {
	obj, err := h.repo.Object(ctx, sess.TenantID, sess.ObjectID)
	if err != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("read object past pending: %w", err))
	}
	if obj.State != statemachine.StateAvailable || obj.Checksum != composite {
		return connect.Errorf(connect.CodeFailedPrecondition,
			"object state %s is not waiting for these parts", obj.State)
	}
	logger.FromContext(ctx).Info("multipart completion retried after its object was promoted",
		zap.String("upload_id", sess.UploadID), zap.String("object_id", sess.ObjectID.String()))
	return nil
}

// assembled is what the store made of the parts, and whether promoting it was
// this call's transition.
type assembled struct {
	etag    string
	size    int64
	changed bool
}

// assemble has the store assemble the parts in the bucket the session was
// anchored to, and settles the object there or, after a storage migration
// rebound its collection, in the bucket it is bound to now (settle).
func (h *Handler) assemble(ctx context.Context, sess Session, backendID, bucket string, args CompleteArgs) (assembled, error) {
	etag, size, err := h.storage.CompleteMultipart(ctx, backendID, bucket, args.TenantID, sess.StorageUploadID, sess.Collection, sess.Key, sess.ChecksumAlgo, args.Parts)
	if err != nil {
		return assembled{}, rpcerr.New(connect.CodeInternal, fmt.Errorf("storage complete: %w", err))
	}
	anchor := objecth.Location{BackendID: backendID, TenantID: args.TenantID, Bucket: bucket, Collection: sess.Collection, Key: sess.Key}
	return h.settle(ctx, sess, anchor, assembled{etag: etag, size: size}, args.UploadID)
}

// afterPromote records the version and the quota of an object this call
// promoted; a retry that found it promoted already records neither.
func (h *Handler) afterPromote(ctx context.Context, sess Session, a assembled, composite string) {
	if !a.changed {
		return
	}
	if h.versions != nil {
		objecth.LogSideEffect(ctx, objecth.EffectVersion, sess.ObjectID, h.versions.OnPromote(ctx, VersionedObject{
			ObjectID:              sess.ObjectID,
			TenantID:              sess.TenantID,
			Collection:            sess.Collection,
			Key:                   sess.Key,
			SizeBytes:             a.size,
			ETag:                  a.etag,
			ContentType:           "", // multipart doesn't carry CT through Storage; lookup later
			ChecksumAlgo:          sess.ChecksumAlgo,
			Checksum:              composite,
			ChecksumPartSizeBytes: sess.PartSizeBytes,
		}))
	}
	// Quota accounting — symmetric with single-PUT path on object.Handler.
	if h.quota == nil {
		return
	}
	if err := h.quota.OnObjectPromoted(ctx, sess.TenantID, sess.ObjectID, a.size); err != nil {
		// Suppressed on purpose — a transient accounting failure must not
		// undo a successful state transition, and the nightly job corrects
		// drift. Logged because that correction is invisible otherwise:
		// without a line here, "usage looks wrong" has no trail back to
		// the writes that were never counted.
		logger.FromContext(ctx).Warn("quota not credited for promoted object",
			zap.String("tenant_id", sess.TenantID.String()),
			zap.Int64("size", a.size), zap.Error(err))
	}
}

// dropSession deletes a session whose upload is settled. A failure does not
// fail the RPC — the upload's outcome stands — but is logged as msg, since a
// systematic one leaves rows behind and nothing else would say so.
func (h *Handler) dropSession(ctx context.Context, uploadID, msg string) {
	if err := h.repo.DeleteSession(ctx, uploadID); err != nil {
		logger.FromContext(ctx).Warn(msg, zap.String("upload_id", uploadID), zap.Error(err))
	}
}

// completedObject reads back the object a completed upload produced; the zero
// Object when that read fails, since the upload itself is done.
func (h *Handler) completedObject(ctx context.Context, tenantID, objectID uuid.UUID, uploadID string) objecth.Object {
	obj, err := h.repo.Object(ctx, tenantID, objectID)
	if err != nil {
		logger.FromContext(ctx).Warn("completed object not read back",
			zap.String("upload_id", uploadID), zap.Error(err))
		return objecth.Object{}
	}
	return obj
}

func (h *Handler) AbortMultipartUpload(ctx context.Context, uploadID string, want SessionRef) (err error) {
	defer func() { h.logOutcome(ctx, opAbort, zapcore.InfoLevel, err, zap.String("upload_id", uploadID)) }()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return err
	}
	sess, err := h.session(ctx, tenantID, uploadID, want)
	if err != nil {
		return err
	}
	objectURI := paladin.ObjectResource(tenantID.String(), sess.Collection, sess.Key)
	if err := auth.AssertCapabilityOp(ctx, limes.OpDelete, objectURI); err != nil {
		return err
	}
	// Through the gate as a read — an abort frees storage, so only a disabled
	// backend refuses it — then authorized on the anchored (backend, bucket),
	// so a bucket:/collection:-scoped PAT enforces on abort.
	backendID, bucket, err := h.target(sess, bucketgate.Read)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, sess.Collection, sess.Key, backendID, bucket, cedar.ActionDeleteObject, 0, ""); err != nil {
		return err
	}
	if err := h.storage.AbortMultipart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.Collection, sess.Key); err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	// The session first, marked as aborted: discarding the object cascades
	// to it, and a cascade records a debt to abort the upload again. A failure
	// here leaves the row to the discard below, with an abort debt that
	// repeats a landed abort.
	h.dropSession(ctx, uploadID, "multipart session not deleted after abort")
	// Discard the underlying object so the reconciler has nothing to promote.
	if err := h.sm.DiscardFailed(ctx, sess.ObjectID, statemachine.FailedMultipartAborted); err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return nil
}

// PresignPart issues a presigned URL for uploading a single part of an
// in-flight multipart session. Authorization is checked against the underlying
// object's (collection, key); the storage URL targets the bucket bound to that
// Collection.
// PresignPart signs one part's URL, bound to the part's exact length and to
// checksumValue under the upload's algorithm.
func (h *Handler) PresignPart(ctx context.Context, uploadID string, partNumber int32, ttl time.Duration, checksumValue string, want SessionRef) (url string, hdrs map[string]string, exp time.Time, err error) {
	// Counted like every other presign: Paladin never proxies bytes, so the
	// URL is the transfer as far as the control plane is concerned.
	start := time.Now()
	defer func() {
		metrics.RecordPresign(ctx, metrics.PresignOpPart, metrics.PresignOutcome(err), time.Since(start).Seconds())
		h.logOutcome(ctx, opPresignPart, zapcore.DebugLevel, err, zap.String("upload_id", uploadID), zap.Int32("part_number", partNumber))
	}()

	tenantID, p, err := apiutil.ActingContext(ctx)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	sess, err := h.session(ctx, tenantID, uploadID, want)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if err := sess.checkPart(partNumber, checksumValue); err != nil {
		return "", nil, time.Time{}, err
	}
	objectURI := paladin.ObjectResource(tenantID.String(), sess.Collection, sess.Key)
	// Presigned part URL grants Put on the underlying object; gate on
	// both OpPresign (the act of issuing a URL) and OpPut (the op the
	// URL ultimately authorises). Either failure short-circuits.
	if err := auth.AssertCapabilityOp(ctx, auth.OpPresign, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpPut, objectURI); err != nil {
		return "", nil, time.Time{}, err
	}
	// Through the write gate before Cedar, then authorized on where the part
	// goes, so a bucket:/collection:-scoped PAT enforces on the part presign.
	backendID, bucket, err := h.target(sess, bucketgate.BytesWrite)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if err := h.authorize(ctx, p, tenantID, sess.Collection, sess.Key, backendID, bucket, cedar.ActionPresignPut, 0, ""); err != nil {
		return "", nil, time.Time{}, err
	}
	return h.signPart(ctx, tenantID, sess, backendID, bucket, PartBinding{
		Number:        partNumber,
		SizeBytes:     sess.PartLength(partNumber),
		ChecksumAlgo:  sess.ChecksumAlgo,
		ChecksumValue: checksumValue,
	}, ttl)
}

// checkPart refuses a part the session cannot sign a URL for: a number
// outside 1..TotalParts, a session from before part URLs were bound to a
// length and a checksum, or a checksum malformed under the session's
// algorithm.
func (s Session) checkPart(partNumber int32, checksumValue string) error {
	if partNumber <= 0 || (s.TotalParts > 0 && partNumber > s.TotalParts) {
		return connect.Errorf(connect.CodeInvalidArgument,
			"part_number %d out of range (1..%d)", partNumber, s.TotalParts)
	}
	// A part URL is bound to the part's length, which needs the object's
	// registered size, and to a checksum, which needs the session's
	// algorithm. A session without them predates the binding.
	if s.SizeBytes <= 0 || s.TotalParts <= 0 || !checksum.Known(s.ChecksumAlgo) {
		return connect.NewError(connect.CodeFailedPrecondition,
			"upload was initiated without a size and checksum algorithm; start a new upload")
	}
	if err := checksum.Validate(s.ChecksumAlgo, checksumValue); err != nil {
		return rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("checksum_value: %w", err))
	}
	return nil
}

// signPart signs the URL for part, living ttl within the bucket's ceiling,
// against (backendID, bucket), where target found the session's parts. The
// anchored bucket's constraints bound the part URL's lifetime.
func (h *Handler) signPart(ctx context.Context, tenantID uuid.UUID, sess Session, backendID, bucket string, part PartBinding, ttl time.Duration) (string, map[string]string, time.Time, error) {
	ttl, err := h.ttl.ResolveWithin(presignttl.OpPart, ttl, uploadpolicy.For(h.limits, sess.BucketConstraints).PutTTLCeiling())
	if err != nil {
		return "", nil, time.Time{}, err
	}
	url, hdrs, exp, err := h.storage.PresignPart(ctx, backendID, bucket, tenantID, sess.StorageUploadID, sess.Collection, sess.Key, part, ttl)
	if err != nil {
		return "", nil, time.Time{}, rpcerr.New(connect.CodeInternal, fmt.Errorf("presign part: %w", err))
	}
	return url, hdrs, exp, nil
}

// checkParts refuses a completion list that is not the upload's parts
// 1..TotalParts, each once and each with a checksum under the session's
// algorithm. A subset would assemble an object shorter than the one admitted;
// the store would accept it, and only the size check at promotion would
// notice.
func (s Session) checkParts(parts []PartETag) error {
	if len(parts) != int(s.TotalParts) {
		return fmt.Errorf("%d parts listed, the upload has %d", len(parts), s.TotalParts)
	}
	seen := make(map[int32]bool, len(parts))
	for _, p := range parts {
		if p.PartNumber < 1 || p.PartNumber > s.TotalParts || seen[p.PartNumber] {
			return fmt.Errorf("part %d is out of range or listed twice", p.PartNumber)
		}
		seen[p.PartNumber] = true
		if err := checksum.Validate(s.ChecksumAlgo, p.ChecksumValue); err != nil {
			return fmt.Errorf("part %d checksum: %w", p.PartNumber, err)
		}
	}
	return nil
}

// compositeChecksum is the composite of the parts' checksums, in part order.
// The parts are those checkParts accepted: each number once, each checksum
// valid under the session's algorithm.
func (s Session) compositeChecksum(parts []PartETag) (string, error) {
	ordered := slices.SortedFunc(slices.Values(parts), func(a, b PartETag) int { return cmp.Compare(a.PartNumber, b.PartNumber) })
	values := make([]string, len(ordered))
	for i, p := range ordered {
		values[i] = p.ChecksumValue
	}
	return checksum.Composite(s.ChecksumAlgo, values)
}

// discardMismatched settles an assembled object whose size is not the one it
// was registered with: its bytes are deleted from every bucket they are in —
// the anchored one, and the collection's when they were moved there — then
// the row is discarded. In that order, so a failure between the two leaves a
// PENDING row the reconciler will HEAD and discard, never a row gone over
// bytes nothing will remove.
func (h *Handler) discardMismatched(ctx context.Context, sess Session, uploadID string, cause error, at ...objecth.Location) error {
	for _, loc := range at {
		if err := h.storage.DeleteObject(ctx, loc.BackendID, loc.Bucket, sess.TenantID, sess.Collection, sess.Key); err != nil {
			return rpcerr.New(connect.CodeInternal, fmt.Errorf("%w; deleting the stored bytes failed: %w", cause, err))
		}
	}
	// The session first, as on abort: discarding the object cascades to it.
	h.dropSession(ctx, uploadID, "multipart session not deleted after a refused complete")
	if err := h.sm.DiscardFailed(ctx, sess.ObjectID, statemachine.FailedContentMismatch); err != nil {
		return rpcerr.New(connect.CodeInternal, fmt.Errorf("%w; discarding the object failed: %w", cause, err))
	}
	return connect.NewError(connect.CodeFailedPrecondition, cause.Error()).WithCause(cause)
}

// ListParts reports which parts of an in-flight upload have actually landed
// in the object store, so a client resuming an interrupted upload knows what
// it still has to send.
//
// The answer comes from the backend, not from a table here. Parts are PUT
// directly to the object store through presigned URLs — Paladin hands out the
// URL and never sees the transfer — so a local journal could only record what
// was authorised, never what arrived, and those differ in exactly the case
// this endpoint exists to serve.
//
// The page token is the last part number seen, matching the S3 contract the
// call is a thin wrapper over.
func (h *Handler) ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string, want SessionRef) (_ []Part, _ string, err error) {
	defer func() { h.logOutcome(ctx, opListParts, zapcore.DebugLevel, err, zap.String("upload_id", uploadID)) }()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, "", err
	}
	sess, err := h.session(ctx, tenantID, uploadID, want)
	if err != nil {
		return nil, "", err
	}
	// The parts all belong to the session's one object, so that object is
	// the resource the listing touches.
	if err := auth.AssertCapabilityOp(ctx, limes.OpList,
		paladin.ObjectResource(tenantID.String(), sess.Collection, sess.Key)); err != nil {
		return nil, "", err
	}
	// Through the gate as a read, then authorized on the anchored (backend,
	// bucket), so a bucket:/collection:-scoped PAT's read scopes enforce on
	// listing parts.
	backendID, bucket, err := h.target(sess, bucketgate.Read)
	if err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, principal, tenantID, sess.Collection, sess.Key, backendID, bucket, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, "", err
	}
	after, err := partsPageAfter(pageToken)
	if err != nil {
		return nil, "", err
	}
	parts, next, err := h.storage.ListMultipartParts(ctx,
		backendID, bucket, tenantID, sess.StorageUploadID,
		sess.Collection, sess.Key, partsPageSize(pageSize), after)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return parts, partsPageToken(next), nil
}

// partsPageSize is the page size ListParts asks the backend for: the
// caller's, unless unset or above the most it lists at once.
func partsPageSize(pageSize int32) int32 {
	if pageSize <= 0 || pageSize > maxPartsPageSize {
		return defaultPartsPageSize
	}
	return pageSize
}

// partsPageAfter is the part number a ListParts page token resumes after; 0
// for the first page.
func partsPageAfter(pageToken string) (int32, error) {
	if pageToken == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(pageToken, 10, 32)
	if err != nil || n < 0 {
		return 0, connect.Errorf(connect.CodeInvalidArgument,
			"page_token must be a part number: %q", pageToken)
	}
	return int32(n), nil
}

// partsPageToken is the token for the page after the part numbered next; ""
// when the backend has no more.
func partsPageToken(next int32) string {
	if next <= 0 {
		return ""
	}
	return strconv.FormatInt(int64(next), 10)
}

// ListParts' page size: unset or above the most S3 lists in one call is the
// default.
const (
	defaultPartsPageSize = 100
	maxPartsPageSize     = 1000
)

// msgTenantMismatch refuses a session another tenant opened.
const msgTenantMismatch = "tenant mismatch"

// session reads the upload uploadID names and checks it is the caller's and
// the object the request names. A session the store does not have is
// NotFound; any other read error is the store failing, which is Internal —
// not an absent upload a client would start over for.
func (h *Handler) session(ctx context.Context, tenantID uuid.UUID, uploadID string, want SessionRef) (Session, error) {
	sess, err := h.repo.GetSession(ctx, uploadID)
	switch {
	case errors.Is(err, ErrUploadNotFound):
		return Session{}, rpcerr.New(connect.CodeNotFound, err)
	case err != nil:
		return Session{}, rpcerr.New(connect.CodeInternal, fmt.Errorf("read upload session: %w", err))
	}
	if sess.TenantID != tenantID {
		return Session{}, connect.NewError(connect.CodePermissionDenied, msgTenantMismatch)
	}
	return sess, assertSessionMatches(sess, want)
}

// logOutcome records how op ended: done at success (info for a change, debug
// for a read), refused at warn, failed at error. fields name what it acted
// on; never a URL, a signed header or a checksum.
func (h *Handler) logOutcome(ctx context.Context, op string, success zapcore.Level, err error, fields ...zap.Field) {
	if h.log == nil {
		return
	}
	fields = append(fields, zap.String("op", op))
	if tenant, terr := auth.EffectiveTenant(ctx); terr == nil {
		fields = append(fields, zap.String("tenant_id", tenant.String()))
	}
	switch code := connect.CodeOf(err); {
	case err == nil:
		h.log.Log(success, "multipart "+op, fields...)
	case serverFault(err):
		h.log.Error("multipart failed", append(fields, zap.Error(err))...)
	default:
		h.log.Warn("multipart refused", append(fields, zap.String("code", code.String()), zap.Error(err))...)
	}
}

// serverFault reports whether err is the server's failure rather than a
// refusal of the request. The bucket gate's refusals are refusals whatever
// their code: a bucket still provisioning answers Unavailable, so the caller
// retries, but nothing on the server failed.
func serverFault(err error) bool {
	if bucketgate.Refused(err) {
		return false
	}
	code := connect.CodeOf(err)
	return code == connect.CodeInternal || code == connect.CodeUnknown || code == connect.CodeUnavailable
}

// authorize runs the Cedar check for a multipart action. backendID/bucket
// carry the resolved physical binding (from the session, or a pre-authz
// LookupBucket on initiate) so the scope-enforcement built-in can confine a
// bucket:/collection:-scoped PAT to its own bucket. Empty backendID/bucket
// leaves the resource without those scope keys, which only ever denies a
// scoped principal — unscoped/roles-only callers are unaffected.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, key, backendID, bucket string, action cedar.Action, sizeBytes int64, contentType string) error {
	return apiutil.Decide(ctx, h.policy, apiutil.CedarPrincipal(p), action,
		&cedar.Resource{TenantID: tenantID, Collection: collection, Key: key, BackendID: backendID, BucketName: bucket, SizeBytes: sizeBytes, ContentType: contentType},
		cedar.RequestContext{SizeBytes: sizeBytes, ContentType: contentType})
}
