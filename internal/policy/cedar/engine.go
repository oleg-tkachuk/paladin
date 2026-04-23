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
	ActionPutObject     = "PutObject"
	ActionPresignPut    = "PresignPut"
	ActionGetObject     = "GetObject"
	ActionPresignGet    = "PresignGet"
	ActionHeadObject    = "HeadObject"
	ActionDeleteObject  = "DeleteObject"
	ActionRestoreObject = "RestoreObject"
	ActionUpdateObject  = "UpdateObject"
	ActionCopyObject    = "CopyObject"
	ActionAdminBucket   = "AdminBucket"
)

// Entity type names — must match the Cedar schema exactly.
const (
	entityTypeTenant = "Tenant"
	entityTypeBucket = "Bucket"
	entityTypeObject = "Object"
	entityTypeUser   = "User"
	entityTypeAction = "Action"
)

// Principal represents the authenticated caller, matching Cedar entity `User`.
type Principal struct {
	Subject  string
	TenantID uuid.UUID
	Roles    []string
}

// Resource is the object or bucket under authorization.
type Resource struct {
	TenantID    uuid.UUID
	BucketID    string
	Key         string // empty for bucket-level actions
	ObjectID    uuid.UUID
	State       string
	SizeBytes   int64
	ContentType string
	Tags        map[string]string
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
// The cache is keyed by (tenant, bucket). Empty bucket means "tenant-level
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
	tenant uuid.UUID
	bucket string
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
				e.compiled.Delete(cacheKey{tenant: ev.TenantID, bucket: ev.BucketID})
			}
		}
	}()
	return nil
}

// IsAuthorized evaluates the applicable policies for (principal, action, resource).
//
// Returns DecisionAllow only when ≥1 `permit` matches AND no `forbid` matches.
// Errors indicate engine faults (policy fetch/compile), not denials.
func (e *Engine) IsAuthorized(ctx context.Context, p *Principal, action string, r *Resource, rc RequestContext) (Decision, error) {
	set, err := e.compiledFor(ctx, r.TenantID, r.BucketID)
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

func (e *Engine) compiledFor(ctx context.Context, tenantID uuid.UUID, bucketID string) (*cedar.PolicySet, error) {
	key := cacheKey{tenant: tenantID, bucket: bucketID}
	if v, ok := e.compiled.Load(key); ok {
		cp := v.(*compiledPolicy)
		if time.Now().Before(cp.expiresAt) {
			e.m.cacheHits.Add(1)
			return cp.policySet, nil
		}
	}
	e.m.cacheMisses.Add(1)

	text, hash, err := e.store.Fetch(ctx, tenantID, bucketID)
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

func userUID(p *Principal) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeUser, cedartypes.String(p.Subject))
}

func tenantUID(tenantID uuid.UUID) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeTenant, cedartypes.String(tenantID.String()))
}

func bucketUID(tenantID uuid.UUID, bucketID string) cedartypes.EntityUID {
	// Namespace by tenant to keep bucket IDs unique across tenants.
	return cedartypes.NewEntityUID(entityTypeBucket, cedartypes.String(tenantID.String()+"/"+bucketID))
}

func resourceUID(r *Resource) cedartypes.EntityUID {
	if r.Key == "" && r.ObjectID == uuid.Nil {
		return bucketUID(r.TenantID, r.BucketID)
	}
	id := r.ObjectID.String()
	if r.ObjectID == uuid.Nil {
		id = r.BucketID + "/" + r.Key
	}
	return cedartypes.NewEntityUID(entityTypeObject, cedartypes.String(id))
}

func actionUID(name string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeAction, cedartypes.String(name))
}

// buildEntities assembles the transient entity graph passed to Authorize.
// PALADIN treats all attribute data as request-scoped — nothing is stored as
// long-lived entities in cedar-go.
func buildEntities(p *Principal, r *Resource) cedartypes.EntityMap {
	tUID := tenantUID(r.TenantID)
	bUID := bucketUID(r.TenantID, r.BucketID)
	uUID := userUID(p)

	tenantEntity := cedartypes.Entity{
		UID: tUID,
		Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
			"display_name": cedartypes.String(""),
			"tier":         cedartypes.String(""),
		}),
	}

	bucketEntity := cedartypes.Entity{
		UID:     bUID,
		Parents: cedartypes.NewEntityUIDSet(tUID),
		Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
			"bucket_id":       cedartypes.String(r.BucketID),
			"storage_backend": cedartypes.String(""),
		}),
	}

	rolesSet := make([]cedartypes.Value, 0, len(p.Roles))
	for _, role := range p.Roles {
		rolesSet = append(rolesSet, cedartypes.String(role))
	}
	userEntity := cedartypes.Entity{
		UID:     uUID,
		Parents: cedartypes.NewEntityUIDSet(tUID),
		Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
			"roles": cedartypes.NewSet(rolesSet...),
		}),
	}

	m := cedartypes.EntityMap{
		tUID: tenantEntity,
		bUID: bucketEntity,
		uUID: userEntity,
	}

	if !(r.Key == "" && r.ObjectID == uuid.Nil) {
		oUID := resourceUID(r)
		tagsSet := make([]cedartypes.Value, 0, len(r.Tags))
		for k := range r.Tags {
			tagsSet = append(tagsSet, cedartypes.String(k))
		}
		m[oUID] = cedartypes.Entity{
			UID:     oUID,
			Parents: cedartypes.NewEntityUIDSet(bUID),
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"key":          cedartypes.String(r.Key),
				"state":        cedartypes.String(r.State),
				"size_bytes":   cedartypes.Long(r.SizeBytes),
				"content_type": cedartypes.String(r.ContentType),
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
