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
// Other services (Bucket, Presign, Multipart, Batch, Tenant, Operation)
// follow the same shape.
package object

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// Storage abstracts S3 / GCS / MinIO. Keep this interface intentionally
// narrow — handler logic does not know which backend it's talking to.
type Storage interface {
	PresignPut(ctx context.Context, args PresignPutArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	PresignPost(ctx context.Context, args PresignPostArgs) (action string, fields map[string]string, expiresAt time.Time, err error)
	PresignGet(ctx context.Context, args PresignGetArgs) (url string, headers map[string]string, expiresAt time.Time, err error)
	Head(ctx context.Context, bucket, key string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
	CopyObject(ctx context.Context, src, dst Location) error
	// DeleteObject is optional — for permanent deletes only.
	DeleteObject(ctx context.Context, bucket, key string) error
	// CompletionMode is derived from the bucket's storage backend config.
	CompletionMode(bucketID string) CompletionMode
}

type CompletionMode uint8

const (
	CompletionModeUnspecified CompletionMode = iota
	CompletionModeImplicit
	CompletionModeExplicit
)

type Location struct {
	Bucket string
	Key    string
}

type PresignPutArgs struct {
	Bucket          string
	Key             string
	ContentType     string
	ChecksumAlgo    string
	SizeHint        int64
	TTL             time.Duration
	RequireChecksum bool
}

type PresignPostArgs struct {
	Bucket       string
	Key          string
	ContentType  string
	MaxSizeBytes int64
	ChecksumAlgo string
	TTL          time.Duration
}

type PresignGetArgs struct {
	Bucket             string
	Key                string
	TTL                time.Duration
	ContentDisposition string
}

// Repository is the persistence seam. Implementations live in internal/store
// (sqlc-backed). Keeping it local to this package keeps handler tests lean.
type Repository interface {
	CreateObject(ctx context.Context, args CreateObjectArgs) (Object, error)
	FindByName(ctx context.Context, tenantID uuid.UUID, bucket, objectID string) (Object, error)
	FindByPath(ctx context.Context, tenantID uuid.UUID, bucket, key string) (Object, error)
	UpdateMetadata(ctx context.Context, args UpdateMetadataArgs) (Object, error)
	ListObjects(ctx context.Context, args ListObjectsArgs) ([]Object, string, error)
	BucketCompletionMode(ctx context.Context, tenantID uuid.UUID, bucket string) (CompletionMode, error)
}

type Object struct {
	ObjectID         uuid.UUID
	TenantID         uuid.UUID
	Bucket           string
	Key              string
	State            statemachine.State
	ContentType      string
	SizeBytes        int64
	ETag             string
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
	Bucket           string
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
	Bucket      string
	PageSize    int32
	PageToken   string
	CompiledCEL cel.Program // pre-compiled; nil = no filter
	OrderBy     string
	SortDesc    bool
}

// Handler is the Connect service implementation.
type Handler struct {
	repo    Repository
	storage Storage
	policy  *cedar.Engine
	filter  *cel.Evaluator
	sm      *statemachine.Transitioner
	presign PresignConfig
}

type PresignConfig struct {
	DefaultTTL     time.Duration
	MaxTTL         time.Duration
	DefaultMaxSize int64
}

func NewHandler(
	repo Repository,
	storage Storage,
	policy *cedar.Engine,
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
	Bucket        string
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

	// 1. Cedar authorization: may this principal PutObject here?
	principal, _ := auth.PrincipalFromContext(ctx)
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: principal.Subject, TenantID: tenantID, Roles: principal.Roles},
		cedar.ActionPresignPut,
		&cedar.Resource{
			TenantID:    tenantID,
			BucketID:    in.Bucket,
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

	// 2. Determine completion mode from bucket's storage backend.
	completion, err := h.repo.BucketCompletionMode(ctx, tenantID, in.Bucket)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

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
		Bucket:           in.Bucket,
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
			Bucket:       in.Bucket,
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
			Bucket:          in.Bucket,
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

	return out, nil
}

// ─── Exemplar RPC: CompleteObject ────────────────────────────────────────────

type CompleteObjectInput struct {
	Name     string // "buckets/{bucket}/objects/{object_id}"
	ETag     string
	Checksum string
}

// CompleteObject is idempotent: if an event has already promoted the object,
// it returns the current state without error. If the object is still PENDING,
// it HEADs the backend to retrieve authoritative etag/size/sequencer.
func (h *Handler) CompleteObject(ctx context.Context, in CompleteObjectInput) (*Object, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	bucket, objectID, err := parseResourceName(in.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	obj, err := h.repo.FindByName(ctx, tenantID, bucket, objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if obj.State == statemachine.StateAvailable {
		// Already promoted (event-driven). No-op.
		return &obj, nil
	}
	if obj.State != statemachine.StatePending {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot complete in state %s", obj.State))
	}

	// Materialize authoritative values via HEAD.
	etag, size, checksum, seq, err := h.storage.Head(ctx, obj.Bucket, obj.Key)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("object not uploaded yet: %w", err))
	}
	if in.ETag != "" && etag != in.ETag {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("etag mismatch"))
	}

	changed, err := h.sm.PromoteToAvailable(ctx, obj.ObjectID, etag, size, checksum, seq, statemachine.SourceRPC)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	_ = changed // idempotent — either way, return fresh object
	fresh, err := h.repo.FindByName(ctx, tenantID, bucket, objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &fresh, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// parseResourceName parses "buckets/{bucket}/objects/{object_id}".
func parseResourceName(name string) (bucket string, objectID uuid.UUID, err error) {
	const prefix = "buckets/"
	if len(name) < len(prefix) || name[:len(prefix)] != prefix {
		return "", uuid.Nil, fmt.Errorf("invalid resource name %q", name)
	}
	rest := name[len(prefix):]
	sep := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			sep = i
			break
		}
	}
	if sep < 0 {
		return "", uuid.Nil, fmt.Errorf("invalid resource name %q", name)
	}
	bucket = rest[:sep]
	remainder := rest[sep+1:]
	const objects = "objects/"
	if len(remainder) < len(objects) || remainder[:len(objects)] != objects {
		return "", uuid.Nil, fmt.Errorf("invalid resource name %q", name)
	}
	id, err := uuid.Parse(remainder[len(objects):])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid object_id in %q: %w", name, err)
	}
	return bucket, id, nil
}

func resolveMaxSize(defaultMax, hint int64) int64 {
	if hint > 0 && hint < defaultMax {
		return hint
	}
	return defaultMax
}

func mapCreateErr(err error) error {
	// Real wiring inspects pgx error codes for 23505 (unique violation).
	// Intentionally stubbed — filled in during the handler sweep.
	return connect.NewError(connect.CodeInternal, err)
}

// ErrVersionMismatch is returned by Repository implementations when an
// optimistic-concurrency update fails (resource_version did not match).
var ErrVersionMismatch = errors.New("resource_version mismatch")
