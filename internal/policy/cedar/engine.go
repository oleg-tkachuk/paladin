// Package cedar wraps the Cedar policy engine for PALADIN authorization decisions.
//
// Decisions are synchronous, in-process, and sub-millisecond. Policies are
// loaded from Postgres (see Store) and compiled on change. Hot-path callers
// get a pre-compiled *cedar.PolicySet via Engine.compiledFor.
package cedar

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
	"github.com/google/uuid"
)

// Decision is the outcome of an authorization check.
type Decision uint8

const (
	DecisionDeny Decision = iota
	DecisionAllow
)

// Action identifiers mirror the Cedar schema (policies/schema.cedarschema).
const (
	// Object-scoped actions (data plane).
	ActionPutObject     = "PutObject"
	ActionPresignPut    = "PresignPut"
	ActionGetObject     = "GetObject"
	ActionPresignGet    = "PresignGet"
	ActionHeadObject    = "HeadObject"
	ActionDeleteObject  = "DeleteObject"
	ActionRestoreObject = "RestoreObject"
	ActionUpdateObject  = "UpdateObject"
	ActionCopyObject    = "CopyObject"

	// ObjectKey-scoped actions (admin plane).
	ActionAdminObjectKey        = "AdminBucket" // legacy alias kept for back-compat
	ActionManageObjectKey       = "ManageObjectKey"
	ActionBindObjectKeyToBucket = "BindObjectKeyToBucket"

	// Backend-scoped actions.
	ActionManageBackend = "ManageBackend"
	ActionReadBackend   = "ReadBackend"

	// Bucket-scoped actions.
	ActionManageBucket = "ManageBucket"
	ActionReadBucket   = "ReadBucket"

	// Tenant-scoped actions.
	ActionManageTenant = "ManageTenant"
	ActionReadTenant   = "ReadTenant"
)

// Entity type names — must match the Cedar schema exactly.
const (
	entityTypeTenant         = "Tenant"
	entityTypeObjectKey      = "ObjectKey"
	entityTypeBucket         = "Bucket"         // physical S3 bucket
	entityTypeStorageBackend = "StorageBackend" // physical backend
	entityTypeObject         = "Object"
	entityTypeUser           = "User"
	entityTypeAction         = "Action"
)

// Principal represents the authenticated caller, matching Cedar entity `User`.
type Principal struct {
	Subject  string
	TenantID uuid.UUID
	Roles    []string
}

// Resource is the entity under authorization. Different fields are
// populated depending on the action target:
//
//   - Object:        TenantID + ObjectKey + Key + ObjectID + bucket fields
//   - ObjectKey:     TenantID + ObjectKey  (+ bucket fields if known)
//   - Bucket:        BackendID + BucketName + (optional OwnerTenantID)
//   - StorageBackend: BackendID
//   - Tenant:        TenantID
//
// The engine uses the populated fields to emit only the relevant Cedar
// entities. Unknown fields stay zero-valued.
type Resource struct {
	// Tenant scope.
	TenantID uuid.UUID

	// ObjectKey + Object.
	ObjectKey   string
	Key         string
	ObjectID    uuid.UUID
	State       string
	SizeBytes   int64
	ContentType string
	Tags        map[string]string

	// Physical bucket + backend (admin plane).
	BackendID     string
	BucketName    string
	OwnerTenantID uuid.UUID // empty = shared bucket
}

// RequestContext carries per-request attributes matched against Cedar context.
type RequestContext struct {
	SizeBytes   int64
	ContentType string
	Now         time.Time
	IP          string
}

// Engine is a thread-safe Cedar authorizer with a compiled-policy cache.
//
// The cache is keyed by (tenant, objectKey). Empty objectKey means "tenant-level
// inherited policy only". Cache entries are invalidated by Store.Watch
// events.
type Engine struct {
	store Store

	// compiled is a sync.Map[cacheKey] *compiledPolicy{hash, policy, expiresAt}
	compiled sync.Map

	// ttl bounds how long a cached compile is served before re-fetch.
	// Set short (~30s) so a missed invalidation event self-corrects.
	ttl time.Duration

	m metrics
}

type cacheKey struct {
	tenant    uuid.UUID
	objectKey string
}

type compiledPolicy struct {
	hash      []byte
	policySet *cedar.PolicySet
	expiresAt time.Time
}

// NewEngine constructs an Engine. Call Start to kick off the invalidation loop.
func NewEngine(store Store, ttl time.Duration) *Engine {
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	return &Engine{store: store, ttl: ttl}
}

// Start begins watching the Store for policy changes. Cancel ctx to stop.
func (e *Engine) Start(ctx context.Context) error {
	events, err := e.store.Watch(ctx)
	if err != nil {
		return fmt.Errorf("cedar: watch store: %w", err)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				e.compiled.Delete(cacheKey{tenant: ev.TenantID, objectKey: ev.ObjectKey})
			}
		}
	}()
	return nil
}

// IsAuthorized evaluates the applicable policies for (principal, action, resource).
//
// Authorizer is the narrow interface handlers depend on. *Engine is the
// production implementation; tests inject a permissive or recording fake.
type Authorizer interface {
	IsAuthorized(ctx context.Context, p *Principal, action string, r *Resource, rc RequestContext) (Decision, error)
}

// Returns DecisionAllow only when ≥1 `permit` matches AND no `forbid` matches.
// Errors indicate engine faults (policy fetch/compile), not denials.
func (e *Engine) IsAuthorized(ctx context.Context, p *Principal, action string, r *Resource, rc RequestContext) (Decision, error) {
	set, err := e.compiledFor(ctx, r.TenantID, r.ObjectKey)
	if err != nil {
		e.m.compileErrs.Add(1)
		return DecisionDeny, err
	}

	entities := buildEntities(p, r)
	req := cedartypes.Request{
		Principal: userUID(p),
		Action:    actionUID(action),
		Resource:  resourceUID(r),
		Context:   buildContext(rc),
	}

	decision, _ := cedar.Authorize(set, entities, req)
	if bool(decision) {
		e.m.authzAllowed.Add(1)
		return DecisionAllow, nil
	}
	e.m.authzDenied.Add(1)
	return DecisionDeny, nil
}

func (e *Engine) compiledFor(ctx context.Context, tenantID uuid.UUID, objectKey string) (*cedar.PolicySet, error) {
	key := cacheKey{tenant: tenantID, objectKey: objectKey}
	if v, ok := e.compiled.Load(key); ok {
		cp := v.(*compiledPolicy)
		if time.Now().Before(cp.expiresAt) {
			e.m.cacheHits.Add(1)
			return cp.policySet, nil
		}
	}
	e.m.cacheMisses.Add(1)

	text, hash, err := e.store.Fetch(ctx, tenantID, objectKey)
	if err != nil {
		return nil, fmt.Errorf("cedar: fetch policy: %w", err)
	}
	set, err := compile(text)
	if err != nil {
		return nil, fmt.Errorf("cedar: compile policy: %w", err)
	}
	cp := &compiledPolicy{
		hash:      hash,
		policySet: set,
		expiresAt: time.Now().Add(e.ttl),
	}
	e.compiled.Store(key, cp)
	return set, nil
}

// compile parses the policy text into a cedar.PolicySet. An empty policy
// set is returned for empty input so that "no policy" == "deny all".
func compile(text string) (*cedar.PolicySet, error) {
	if text == "" {
		return cedar.NewPolicySet(), nil
	}
	return cedar.NewPolicySetFromBytes("", []byte(text))
}

// Validate parses the policy text and returns the parser error (or nil).
// Exposed for pre-save UI validation; does not persist or compile into cache.
func Validate(text string) error {
	_, err := compile(text)
	return err
}

func userUID(p *Principal) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeUser, cedartypes.String(p.Subject))
}

func tenantUID(tenantID uuid.UUID) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeTenant, cedartypes.String(tenantID.String()))
}

func objectKeyUID(tenantID uuid.UUID, objectKey string) cedartypes.EntityUID {
	// Namespace by tenant to keep objectKey IDs unique across tenants.
	return cedartypes.NewEntityUID(entityTypeObjectKey, cedartypes.String(tenantID.String()+"/"+objectKey))
}

func physicalBucketUID(backendID, bucketName string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeBucket, cedartypes.String(backendID+"/"+bucketName))
}

func storageBackendUID(backendID string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeStorageBackend, cedartypes.String(backendID))
}

// resourceUID picks the most-specific entity type populated on the resource:
//   - Object        when Key/ObjectID set
//   - ObjectKey     when ObjectKey set (without Object)
//   - Bucket        when BackendID+BucketName set (without ObjectKey)
//   - StorageBackend when only BackendID set
//   - Tenant         when only TenantID set (admin tenant ops)
func resourceUID(r *Resource) cedartypes.EntityUID {
	if r.Key != "" || r.ObjectID != uuid.Nil {
		id := r.ObjectID.String()
		if r.ObjectID == uuid.Nil {
			id = r.ObjectKey + "/" + r.Key
		}
		return cedartypes.NewEntityUID(entityTypeObject, cedartypes.String(id))
	}
	if r.ObjectKey != "" {
		return objectKeyUID(r.TenantID, r.ObjectKey)
	}
	if r.BackendID != "" && r.BucketName != "" {
		return physicalBucketUID(r.BackendID, r.BucketName)
	}
	if r.BackendID != "" {
		return storageBackendUID(r.BackendID)
	}
	return tenantUID(r.TenantID)
}

func actionUID(name string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeAction, cedartypes.String(name))
}

// buildEntities assembles the transient entity graph passed to Authorize.
// PALADIN treats all attribute data as request-scoped — nothing is stored as
// long-lived entities in cedar-go.
//
// Entities are emitted only when the corresponding resource fields are
// populated, so admin-plane requests against a StorageBackend don't bring
// along an unrelated Tenant entity that the policy never references.
func buildEntities(p *Principal, r *Resource) cedartypes.EntityMap {
	uUID := userUID(p)
	rolesSet := make([]cedartypes.Value, 0, len(p.Roles))
	for _, role := range p.Roles {
		rolesSet = append(rolesSet, cedartypes.String(role))
	}

	m := cedartypes.EntityMap{}

	// Tenant — emitted whenever a tenant is in scope (either the principal's
	// or the resource's). Most data-plane calls hit this branch.
	var tUID cedartypes.EntityUID
	if r.TenantID != uuid.Nil {
		tUID = tenantUID(r.TenantID)
		m[tUID] = cedartypes.Entity{
			UID: tUID,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"tenant_id":    cedartypes.String(r.TenantID.String()),
				"display_name": cedartypes.String(""),
				"labels":       cedartypes.NewSet(),
			}),
		}
	}

	// User — anchors the principal under their tenant when known.
	userParents := cedartypes.EntityUIDSet{}
	if r.TenantID != uuid.Nil {
		userParents = cedartypes.NewEntityUIDSet(tUID)
	}
	m[uUID] = cedartypes.Entity{
		UID:     uUID,
		Parents: userParents,
		Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
			"subject":   cedartypes.String(p.Subject),
			"tenant_id": cedartypes.String(p.TenantID.String()),
			"roles":     cedartypes.NewSet(rolesSet...),
		}),
	}

	// StorageBackend — admin-plane only.
	var sbUID cedartypes.EntityUID
	if r.BackendID != "" {
		sbUID = storageBackendUID(r.BackendID)
		m[sbUID] = cedartypes.Entity{
			UID: sbUID,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"backend_id": cedartypes.String(r.BackendID),
			}),
		}
	}

	// Bucket (physical) — child of StorageBackend.
	var bUID cedartypes.EntityUID
	if r.BackendID != "" && r.BucketName != "" {
		bUID = physicalBucketUID(r.BackendID, r.BucketName)
		bucketParents := cedartypes.NewEntityUIDSet(sbUID)
		bucketEntity := cedartypes.Entity{
			UID:     bUID,
			Parents: bucketParents,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"bucket_name":     cedartypes.String(r.BucketName),
				"backend_id":      cedartypes.String(r.BackendID),
				"owner_tenant_id": cedartypes.String(r.OwnerTenantID.String()),
				"labels":          cedartypes.NewSet(),
			}),
		}
		m[bUID] = bucketEntity
	}

	// ObjectKey — child of Tenant (and Bucket when bucket is in scope).
	var okUID cedartypes.EntityUID
	if r.ObjectKey != "" && r.TenantID != uuid.Nil {
		okUID = objectKeyUID(r.TenantID, r.ObjectKey)
		parents := cedartypes.NewEntityUIDSet(tUID)
		if r.BackendID != "" && r.BucketName != "" {
			parents = cedartypes.NewEntityUIDSet(tUID, bUID)
		}
		m[okUID] = cedartypes.Entity{
			UID:     okUID,
			Parents: parents,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"object_key":  cedartypes.String(r.ObjectKey),
				"tenant_id":   cedartypes.String(r.TenantID.String()),
				"bucket_name": cedartypes.String(r.BucketName),
				"backend_id":  cedartypes.String(r.BackendID),
			}),
		}
	}

	// Object — child of ObjectKey.
	if r.Key != "" || r.ObjectID != uuid.Nil {
		oUID := resourceUID(r)
		tagsSet := make([]cedartypes.Value, 0, len(r.Tags))
		for k := range r.Tags {
			tagsSet = append(tagsSet, cedartypes.String(k))
		}
		var parents cedartypes.EntityUIDSet
		if okUID != (cedartypes.EntityUID{}) {
			parents = cedartypes.NewEntityUIDSet(okUID)
		}
		m[oUID] = cedartypes.Entity{
			UID:     oUID,
			Parents: parents,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"key":          cedartypes.String(r.Key),
				"state":        cedartypes.String(r.State),
				"size_bytes":   cedartypes.Long(r.SizeBytes),
				"content_type": cedartypes.String(r.ContentType),
				"tenant_id":    cedartypes.String(r.TenantID.String()),
				"object_key":   cedartypes.String(r.ObjectKey),
				"bucket_name":  cedartypes.String(r.BucketName),
				"backend_id":   cedartypes.String(r.BackendID),
				"tags":         cedartypes.NewSet(tagsSet...),
			}),
		}
	}

	return m
}

func buildContext(rc RequestContext) cedartypes.Record {
	now := rc.Now
	if now.IsZero() {
		now = time.Now()
	}
	return cedartypes.NewRecord(cedartypes.RecordMap{
		"size_bytes":   cedartypes.Long(rc.SizeBytes),
		"content_type": cedartypes.String(rc.ContentType),
		"now":          cedartypes.Long(now.Unix()),
		"ip":           cedartypes.String(rc.IP),
	})
}

// metrics reserves counters the production wire-up should export via OTEL.
type metrics struct {
	authzAllowed atomic.Uint64
	authzDenied  atomic.Uint64
	compileErrs  atomic.Uint64
	cacheHits    atomic.Uint64
	cacheMisses  atomic.Uint64
}

// Stats returns a snapshot of the internal metrics.
func (e *Engine) Stats() (allowed, denied, compileErrs, hits, misses uint64) {
	return e.m.authzAllowed.Load(), e.m.authzDenied.Load(), e.m.compileErrs.Load(), e.m.cacheHits.Load(), e.m.cacheMisses.Load()
}
