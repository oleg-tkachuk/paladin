// Package admindomain holds the handler-facing v2 domain types for admin
// services. These types are independent of proto and sqlc — handlers consume
// them, adapters produce them, connectshim wraps them for the wire.
package admindomain

import (
	"time"

	"github.com/google/uuid"
)

// ─── Storage backend ────────────────────────────────────────────────────────

type StorageBackend struct {
	BackendID            string
	DisplayName          string
	Kind                 string // "aws-s3" | "s3-compatible" | "gcs"
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
	// rejects every PALADIN-mediated operation that resolves to it. Operator
	// managed via SetBackendEnabled; never mirrored from static config.
	Enabled bool
	// ReadOnly is the drain state (migration 047). When true on an enabled
	// backend, reads / presign-GET / HEAD / list still resolve but mutations
	// (PUT / POST / multipart-init / copy-dest / update / delete / version
	// writes) are refused, so an operator can migrate data off before fully
	// disabling. Operator-managed via SetBackendReadOnly; not config-mirrored.
	ReadOnly bool
	// Maintenance is the OPERATOR-SET, advisory flag (migration 049): a label
	// signalling "under maintenance" surfaced in the UI. Unlike enabled /
	// read_only it does NOT gate operations; unlike Health it's operator-set,
	// not derived. Operator-managed via SetBackendMaintenance; not config-mirrored.
	Maintenance bool
	// Health is the DERIVED, advisory health state (migration 048): the
	// outcome of the last TestBackend probe. "unknown" | "ok" | "error".
	// Surfaced in the UI but NOT a gate — the operator decides whether to
	// disable/drain in response. HealthMessage carries the probe error when
	// status is "error"; HealthCheckedAt is the instant of the last probe
	// (zero until first probed).
	HealthStatus    string
	HealthMessage   string
	HealthCheckedAt time.Time
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ServerSideEncryption struct {
	Type  string // "" | "AES256" | "KMS"
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
	ProvisionState  string
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
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

type BucketConstraints struct {
	MaxObjectSizeBytes        int64
	MinPartSizeBytes          int64
	MaxPartSizeBytes          int64
	MaxParts                  int32
	AllowedContentTypes       []string
	MaxPresignPutTTL          time.Duration
	MaxPresignGetTTL          time.Duration
	RequiredChecksumAlgorithm string // "" | "CRC32C" | "SHA256" | "MD5"
}

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
}

// ─── Quota ──────────────────────────────────────────────────────────────────

type Quota struct {
	QuotaID           uuid.UUID
	TenantID          uuid.UUID // uuid.Nil → bucket-scoped
	BackendID         string    // empty → tenant-scoped
	BucketName        string
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

type EventSubscription struct {
	SubscriptionID  uuid.UUID
	TenantID        uuid.UUID
	CELFilter       string
	SinkKind        string // "http" | "kafka" | "sqs"
	SinkConfig      []byte // JSONB; shape varies by kind
	Disabled        bool
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
