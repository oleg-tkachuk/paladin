// Package admindomain holds the handler-facing v2 domain types for admin
// services. These types are independent of proto and sqlc — handlers consume
// them, adapters produce them, connectshim wraps them for the wire.
package admindomain

import (
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// ─── Storage backend ────────────────────────────────────────────────────────

type StorageBackend struct {
	BackendID   string
	DisplayName string
	Kind        string // "aws-s3" | "s3-compatible" | "gcs"
	// Provider is the vendor/implementation behind Kind — free-form slug
	// ("garage" | "seaweedfs" | "minio" | "aws" | "gcp" | "digitalocean" | …).
	// Kind is too coarse (every self-hosted S3 is "s3-compatible"); Provider
	// records which one, for UI display and vendor-specific handling. Mirrored
	// from static config; empty when unset (the UI falls back to an endpoint
	// heuristic).
	Provider             string
	Endpoint             string
	PublicEndpoint       string
	Region               string
	ForcePathStyle       bool
	CredentialsSecretRef string
	// PreviousCredentialsSecretRef + PreviousCredentialsValidUntil carry the
	// grace-window state after RotateCredentials(grace>0): the prior secret ref
	// and the instant it stays valid until. Empty/zero outside a window.
	PreviousCredentialsSecretRef  string
	PreviousCredentialsValidUntil time.Time
	SSE                           ServerSideEncryption
	Events                        EventSourceConfig
	CedarPolicy                   string
	// Enabled is the durable enable/disable state. false → the backend
	// rejects every Paladin-mediated operation that resolves to it. Operator
	// managed via SetBackendEnabled; never mirrored from static config.
	Enabled bool
	// ReadOnly is the drain state (the schema baseline (001_initial_schema.sql)). When true on an enabled
	// backend, reads / presign-GET / HEAD / list still resolve but mutations
	// (PUT / POST / multipart-init / copy-dest / update / delete / version
	// writes) are refused, so an operator can migrate data off before fully
	// disabling. Operator-managed via SetBackendReadOnly; not config-mirrored.
	ReadOnly bool
	// Maintenance is the OPERATOR-SET, advisory flag (the schema baseline (001_initial_schema.sql)): a label
	// signalling "under maintenance" surfaced in the UI. Unlike enabled /
	// read_only it does NOT gate operations; unlike Health it's operator-set,
	// not derived. Operator-managed via SetBackendMaintenance; not config-mirrored.
	Maintenance bool
	// Declared reports the backend is in the server's storage.backends, the
	// only backends the data plane and the worker build clients for. Set by
	// the backend handler on every read; never stored.
	Declared bool
	// Health is the DERIVED, advisory health state (the schema baseline (001_initial_schema.sql)): the
	// outcome of the last TestBackend probe. "unknown" | "ok" | "error".
	// Surfaced in the UI but NOT a gate — the operator decides whether to
	// disable/drain in response. HealthMessage carries the probe error when
	// status is "error"; HealthCheckedAt is the instant of the last probe
	// (zero until first probed).
	HealthStatus    string
	HealthMessage   string
	HealthCheckedAt time.Time
	// Features is what the last TestBackend probe found for each S3 feature
	// (ADR-0026), as recorded: a feature never probed has no entry.
	Features        []features.Result
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SSETypeKMS is ServerSideEncryption.Type for SSE-KMS, whatever spelling
// the configuration or the API used ("aws:kms", SSE_TYPE_KMS).
const SSETypeKMS = "KMS"

type ServerSideEncryption struct {
	Type  string // "" | "AES256" | SSETypeKMS
	KeyID string
}

type EventSourceConfig struct {
	Enabled      bool
	Target       string // "" | "sqs" | "redis"
	QueueURL     string
	PollInterval time.Duration
}

// ─── Bucket ─────────────────────────────────────────────────────────────────

type Bucket struct {
	BackendID      string
	BucketName     string
	DisplayName    string
	Region         string
	Labels         map[string]string
	OwnerTenantID  uuid.UUID // uuid.Nil = shared
	CedarPolicy    string
	Constraints    BucketConstraints
	LifecycleRules []LifecycleRule
	ObjectLock     ObjectLockConfig
	Versioning     BucketVersioning
	Replication    BucketReplication
	// ProvisionState is the outbox status of the underlying physical
	// bucket: empty/"ready" means the row is committed AND the backend
	// confirms the bucket exists; "pending" means the reconciler still
	// owes the backend a CreateBucket call; "failed" means a non-retryable
	// error stopped the reconciler. Currently used only on Create input —
	// Get / List do not populate it.
	ProvisionState string
	// PublicRead: every object is served to unsigned GETs (ADR-0027). Fixed
	// at creation.
	PublicRead bool
	// PublicBaseURL is where a CDN serves a public bucket; "" means the
	// backend's public endpoint. Fixed at creation.
	PublicBaseURL string
	// CreatedOnBackend: Paladin creates the bucket on its backend
	// (provision_on_backend), rather than taking one that already existed.
	// Only such a bucket may be deleted there. Set when the row is written;
	// never client-settable.
	CreatedOnBackend bool
	ResourceVersion  int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// BucketProvisionRow is the worker's view of a bucket that owes the
// backend a CreateBucket call. Fields are kept narrow on purpose so
// the SELECT stays cheap and the worker doesn't need to know about
// cedar / lifecycle / etc.
type BucketProvisionRow struct {
	BackendID         string
	BucketName        string
	Region            string
	ProvisionState    string
	ProvisionAttempts int32
	LastProvisionAt   time.Time
	// OwnerTenantID is the dedicated-bucket owner (uuid.Nil for shared buckets).
	// The reconciler tags the bucket with it for cost attribution (ADR-0015).
	OwnerTenantID uuid.UUID
	// PublicRead: the reconciler sets the anonymous-read policy before it
	// marks the bucket ready (ADR-0027).
	PublicRead bool
}

// Provision-state constants — mirror the CHECK constraint in the
// migration. Keep these as the single source of truth for state names
// inside Go code so a typo doesn't silently keep the row in 'pending'
// forever.
const (
	BucketProvisionStatePending        = "pending"
	BucketProvisionStateReady          = "ready"
	BucketProvisionStateFailed         = "failed"
	BucketProvisionStateDeleting       = "deleting"
	BucketProvisionStateDeletionFailed = "deletion_failed"
)

// BucketConstraints is defined where it is enforced; the admin plane stores
// the same struct, so the JSON keys in buckets.constraints have one spelling.
type BucketConstraints = uploadpolicy.BucketConstraints

type LifecycleRule struct {
	ID         string
	Enabled    bool
	Match      string // CEL
	Transition *LifecycleTransition
	Expiration *LifecycleExpiration
}

type LifecycleTransition struct {
	After        time.Duration
	StorageClass string
}

type LifecycleExpiration struct {
	After time.Duration
}

type ObjectLockConfig struct {
	Enabled          bool
	DefaultMode      string // "GOVERNANCE" | "COMPLIANCE"
	DefaultRetention time.Duration
}

type BucketVersioning struct {
	Enabled            bool
	KeepDeletesForever bool
}

type BucketReplication struct {
	Enabled           bool
	DestinationBucket string // resource name
	Filter            string // CEL
}

// ─── Audit ──────────────────────────────────────────────────────────────────

type AuditEntry struct {
	EntryID       uuid.UUID
	At            time.Time
	ActorSubject  string
	ActorTenantID uuid.UUID
	ActorAudience string
	Action        string
	ResourceName  string
	RequestID     string
	SourceIP      string
	BeforeJSON    []byte
	AfterJSON     []byte
	ErrorMessage  string

	// CapabilityID is the ID of the capability token attached to the
	// request, if any. Threaded by middleware.Audit from
	// auth.CapabilityFromContext. uuid.Nil when the call was
	// JWT- or API-token-authenticated (no capability presented).
	// Required for per-tool-call rollups in the agentic-plane
	// positioning — every MCP / agent action gets attributed to the
	// cap that authorised it without joining across capability_records
	// by request_id.
	CapabilityID uuid.UUID

	// ResourceTenantID is the tenant the action was done to, as read back
	// from the log. Writers leave it unset; it is derived from ResourceName
	// on insert (ResourceTenant). uuid.Nil when the name carries none.
	ResourceTenantID uuid.UUID
}

// ResourceTenant is the tenant this entry's resource sits under, or uuid.Nil.
// It is what places a platform admin's work inside tenant X in X's trail,
// whose actor tenant is the platform's.
func (e AuditEntry) ResourceTenant() uuid.UUID {
	id, _ := apiutil.TenantInResourceName(e.ResourceName)
	return id
}

// ─── Quota ──────────────────────────────────────────────────────────────────

type Quota struct {
	QuotaID    uuid.UUID
	TenantID   uuid.UUID // uuid.Nil → bucket-scoped
	BackendID  string    // empty → tenant-scoped
	BucketName string
	// OwnerTenantID is the tenant owning a bucket quota's bucket; uuid.Nil
	// for a shared bucket and for every tenant quota. It is where the
	// bucket quota's events go, not its scope.
	OwnerTenantID     uuid.UUID
	MaxTotalBytes     int64
	MaxObjectCount    int64
	MaxBytesPerDay    int64
	MaxObjectsPerDay  int64
	UsageTotalBytes   int64
	UsageObjectCount  int64
	UsageBytesToday   int64
	UsageObjectsToday int64
	LastResetAt       *time.Time
	ResourceVersion   int64
	UpdatedAt         time.Time
}

// ─── Event subscription ─────────────────────────────────────────────────────

// The update_mask paths UpdateSubscription applies: the EventSubscription
// message's proto field names, which the shim checks and the repository reads.
// They differ from the column names (cel_filter, sink_kind, sink_config); the
// repository once read those instead, and every filter or sink edit was
// silently dropped.
const (
	EventSubscriptionPathFilter   = "filter"
	EventSubscriptionPathSink     = "sink"
	EventSubscriptionPathDisabled = "disabled"
)

type EventSubscription struct {
	SubscriptionID  uuid.UUID
	TenantID        uuid.UUID
	CELFilter       string
	SinkKind        string // "http" | "nats" | "kafka" | "sqs" | "rabbitmq"
	SinkConfig      []byte // JSONB; shape varies by kind
	Disabled        bool
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
