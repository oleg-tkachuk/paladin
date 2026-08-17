// Package cedar wraps the Cedar policy engine for PALADIN authorization decisions.
//
// Decisions are synchronous, in-process, and sub-millisecond. Policies are
// loaded from Postgres (see Store) and compiled on change. Hot-path callers
// get a pre-compiled *cedar.PolicySet via Engine.compiledFor.
package cedar

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
	"github.com/google/uuid"
	"go.uber.org/zap"
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
	ActionManageObjectKey       = "ManageObjectKey"
	ActionBindObjectKeyToBucket = "BindObjectKeyToBucket"

	// Backend-scoped actions.
	ActionManageBackend = "ManageBackend"
	ActionReadBackend   = "ReadBackend"

	// Bucket-scoped actions.
	ActionManageBucket = "ManageBucket"
	ActionReadBucket   = "ReadBucket"

	// EnsureTenantStorage — data-plane self-provisioning. A tenant member
	// (including an api_token principal, aud=data) idempotently ensures its
	// OWN shared bucket + object-keys without a platform-admin credential.
	// The handler forces the resource tenant to the caller's own tenant, so
	// the default-policy permit is self-scoped via `principal in Tenant`
	// membership — the same gate that admits the PAT for PresignPut.
	ActionEnsureTenantStorage = "EnsureTenantStorage"

	// Granular bucket sub-actions. Splitting ManageBucket lets compliance
	// roles get fine-grained authority — e.g. ConfigureLock without
	// SetReplication (data-residency risk) — without granting full bucket
	// ownership.
	ActionConfigureBucketPolicy = "ConfigureBucketPolicy"
	ActionConfigureLifecycle    = "ConfigureLifecycle"
	ActionConfigureLock         = "ConfigureLock"
	ActionConfigureVersioning   = "ConfigureVersioning"
	ActionConfigureReplication  = "ConfigureReplication"

	// Sensitive backend ops. Separate action so a "secrets.rotator" role
	// can rotate credentials without inheriting full ManageBackend rights.
	ActionRotateBackendCredentials = "RotateBackendCredentials"

	// Policy engine introspection. Gates ValidatePolicy / SimulateAuthz /
	// GetEffectivePolicy — these leak schema/policy text and shouldn't be
	// open to any authenticated principal.
	ActionInspectPolicy = "InspectPolicy"

	// Tenant-scoped actions.
	ActionManageTenant = "ManageTenant"
	ActionReadTenant   = "ReadTenant"

	// IAM User-scoped actions (manage other users, not the principal).
	ActionManageUser    = "ManageUser"
	ActionReadUser      = "ReadUser"
	ActionResetPassword = "ResetPassword"
	ActionGrantScopes   = "GrantScopes"

	// User-settings actions. Resource is the User entity (the user whose
	// settings are read/written). The principal-as-target case (a user
	// editing their own settings) is the common path — handlers short-circuit
	// to allow without a Cedar round-trip when subject matches. Cedar still
	// gates the cross-user case (admin viewing a teammate's timezone).
	ActionReadUserSettings   = "ReadUserSettings"
	ActionManageUserSettings = "ManageUserSettings"

	// Quota-scoped actions. Resource is the Tenant or Bucket entity (no
	// dedicated Quota entity — quota config attaches 1:1 to the parent).
	ActionManageQuota     = "ManageQuota"
	ActionReadQuota       = "ReadQuota"
	ActionResetQuotaUsage = "ResetQuotaUsage"

	// AuditLog-scoped actions. Resource is the Tenant entity (audit lines
	// are tenant-scoped via actor_tenant_id).
	ActionReadAuditLog   = "ReadAuditLog"
	ActionExportAuditLog = "ExportAuditLog"

	// EventSubscription-scoped actions. Resource is the Tenant entity.
	ActionManageSubscription = "ManageSubscription"
	ActionReadSubscription   = "ReadSubscription"
	ActionTestSubscription   = "TestSubscription"

	// Operation-scoped actions (long-running async ops: BatchDelete /
	// BatchCopy / etc.). Resource is the Tenant entity carrying the
	// op's tenant_id.
	ActionReadOperation   = "ReadOperation"
	ActionCancelOperation = "CancelOperation"

	// OAuth consent (ADR-0009). Checked at /oauth/authorize when a user
	// approves an OAuth client. Resource is the Tenant entity; the request
	// context carries oauth_client_id + oauth_scopes so a tenant policy can
	// forbid specific clients/scopes. Permitted by default for any
	// authenticated principal via the built-in policy.
	ActionAuthorizeOAuth = "AuthorizeOAuth"

	// Billing-scoped actions. Resource is the Tenant entity. Used by
	// BillingService (admin plane) over the charges ledger from
	// migration 027.
	ActionReadBilling = "ReadBilling"
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
//
// TenantSlug is optional. When set, it becomes the canonical Cedar `Tenant::"…"`
// UID key (so policies read `Tenant::"acme"` rather than the UUID). The UUID
// stays as a `tenant_id` attribute for policies that key on it. When unset,
// the UID falls back to the UUID string for backwards compatibility.
type Principal struct {
	Subject    string
	TenantID   uuid.UUID
	TenantSlug string
	// Kind is the wire name of the credential type behind this principal
	// ("user", "api_key", "service_account", "capability") — see
	// auth.PrincipalKind.String(). Exposed to Cedar as `principal.kind` so a
	// policy can distinguish a machine credential from a person: the built-in
	// delete permit turns on exactly that difference.
	//
	// Empty when the constructing path does not carry it. A policy comparing
	// against a kind then matches nothing, which is the fail-closed default.
	Kind  string
	Roles []string
	// Scopes are the JWT-carried scope strings (already in wire form,
	// e.g. "objects:read:tenant_id/object_key/key"). Exposed to Cedar as
	// `principal.scopes` so policies can match scope prefixes for
	// fine-grained delegation. Empty when the principal carries roles only.
	Scopes []string
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
	TenantID   uuid.UUID
	TenantSlug string // optional; preferred for Cedar Tenant UID when set

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

	// IAM target identity (the user BEING managed — distinct from the
	// principal, who is always a User keyed by Subject).
	TargetUserID  uuid.UUID
	TargetSubject string // user's login subject — exposed to Cedar as resource.subject
}

// RequestContext carries per-request attributes matched against Cedar context.
type RequestContext struct {
	SizeBytes   int64
	ContentType string
	Now         time.Time
	IP          string

	// OAuth consent context (ADR-0009): the client being authorized and the
	// scopes it requested. Exposed to policies as context.oauth_client_id +
	// context.oauth_scopes so a tenant can forbid specific clients/scopes.
	// Zero values for non-OAuth checks.
	OAuthClientID string
	OAuthScopes   []string
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

	// canonicalObjectKeyEUID switches the ObjectKey entity UID from the legacy
	// `{tenant_uuid}/{object_key}` form to the canonical A-shape name
	// (ADR-0010). Only takes effect where (backend, bucket) are in scope on the
	// request; attribute/parent-based policies are unaffected by the UID string
	// either way. Default off; flip per-environment after confirming no policy
	// hardcodes a `resource == ObjectKey::"…"` literal.
	canonicalObjectKeyEUID bool

	// log surfaces policy-EVALUATION errors (a policy that fails to evaluate is
	// SKIPPED by cedar — a skipped forbid could otherwise flip a deny to an
	// allow). Nil-safe: NewEngine defaults it to a no-op.
	log *zap.Logger

	m metrics
}

// EngineOption configures an Engine at construction.
type EngineOption func(*Engine)

// WithCanonicalObjectKeyEUID enables the canonical A-shape ObjectKey entity
// UID (ADR-0010, Phase 1). Off by default.
func WithCanonicalObjectKeyEUID(on bool) EngineOption {
	return func(e *Engine) { e.canonicalObjectKeyEUID = on }
}

// WithLogger wires a logger so policy-evaluation errors are surfaced (not just
// counted). Default: no-op.
func WithLogger(l *zap.Logger) EngineOption {
	return func(e *Engine) {
		if l != nil {
			e.log = l
		}
	}
}

type cacheKey struct {
	tenant    uuid.UUID
	objectKey string
}

type compiledPolicy struct {
	hash      []byte
	policySet *cedar.PolicySet
	// tenantSlug is the DB-authoritative slug for this policy's tenant
	// (ADR-0012). Cached with the policy so keying tenant membership on the
	// trusted slug costs no extra query on the authz hot path.
	tenantSlug string
	// perObjectEval is true when at least one policy in the set reads a
	// per-object resource attribute (analysed once at compile time). List
	// handlers use it to decide between a single objectKey-scoped Cedar check
	// and a per-row check. See NeedsPerObjectEval.
	perObjectEval bool
	expiresAt     time.Time
}

// NewEngine constructs an Engine. Call Start to kick off the invalidation loop.
func NewEngine(store Store, ttl time.Duration, opts ...EngineOption) *Engine {
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	e := &Engine{store: store, ttl: ttl, log: zap.NewNop()}
	for _, o := range opts {
		o(e)
	}
	return e
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
				if ev.ResyncAll {
					// The watcher reconnected after a dropped LISTEN and may
					// have missed invalidations. Drop the whole compiled cache;
					// every scope re-fetches on next use (bounded staleness
					// ends now instead of at the TTL).
					e.m.watchResyncs.Add(1)
					e.compiled.Range(func(k, _ any) bool {
						e.compiled.Delete(k)
						return true
					})
					continue
				}
				if ev.ObjectKey == "" {
					// Tenant-level change: the inherited text is concatenated
					// into every objectKey-scoped compile, so drop all of the
					// tenant's entries, not just the tenant-level one.
					e.compiled.Range(func(k, _ any) bool {
						if k.(cacheKey).tenant == ev.TenantID {
							e.compiled.Delete(k)
						}
						return true
					})
				} else {
					e.compiled.Delete(cacheKey{tenant: ev.TenantID, objectKey: ev.ObjectKey})
				}
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
	// compiledFor loads the resource-tenant's policy by the TRUSTED UUID and
	// returns that tenant's DB-authoritative slug. The slug — never the
	// JWT-supplied one — keys tenant membership in the entity graph (ADR-0012),
	// so a spoofed tenant_slug claim cannot satisfy a member permit.
	set, authSlug, err := e.compiledFor(ctx, r.TenantID, r.ObjectKey)
	if err != nil {
		e.m.compileErrs.Add(1)
		return DecisionDeny, err
	}

	entities := e.buildEntities(p, r, authSlug)
	req := cedartypes.Request{
		Principal: userUID(p),
		Action:    actionUID(action),
		Resource:  e.resourceUID(r, authSlug),
		Context:   buildContext(rc),
	}

	decision, diag := cedar.Authorize(set, entities, req)
	if len(diag.Errors) > 0 {
		// A policy that errors at evaluation is SKIPPED by cedar — including,
		// possibly, a `forbid` that should have matched, which would flip a
		// deny into an accidental ALLOW. Never let an eval error become an
		// allow: fail closed, count it, and log which policies faulted so the
		// broken policy gets fixed rather than silently mis-authorising.
		e.m.evalErrs.Add(1)
		e.log.Warn("cedar: policy evaluation errors; denying (fail-closed)",
			zap.String("action", action),
			zap.Int("error_count", len(diag.Errors)),
			zap.String("errors", fmt.Sprint(diag.Errors)),
		)
		e.m.authzDenied.Add(1)
		return DecisionDeny, nil
	}
	if bool(decision) {
		e.m.authzAllowed.Add(1)
		return DecisionAllow, nil
	}
	e.m.authzDenied.Add(1)
	return DecisionDeny, nil
}

func (e *Engine) compiledFor(ctx context.Context, tenantID uuid.UUID, objectKey string) (*cedar.PolicySet, string, error) {
	cp, err := e.loadCompiled(ctx, tenantID, objectKey)
	if err != nil {
		return nil, "", err
	}
	return cp.policySet, cp.tenantSlug, nil
}

// loadCompiled returns the cached (or freshly fetched + compiled) policy entry
// for the scope, refreshing on TTL expiry. The per-object-eval flag is analysed
// once here, on the compile path, so the authz hot path never re-inspects the
// policy AST.
func (e *Engine) loadCompiled(ctx context.Context, tenantID uuid.UUID, objectKey string) (*compiledPolicy, error) {
	key := cacheKey{tenant: tenantID, objectKey: objectKey}
	if v, ok := e.compiled.Load(key); ok {
		cp := v.(*compiledPolicy)
		if time.Now().Before(cp.expiresAt) {
			e.m.cacheHits.Add(1)
			return cp, nil
		}
	}
	e.m.cacheMisses.Add(1)

	text, hash, slug, err := e.store.Fetch(ctx, tenantID, objectKey)
	if err != nil {
		return nil, fmt.Errorf("cedar: fetch policy: %w", err)
	}
	set, err := compile(text)
	if err != nil {
		return nil, fmt.Errorf("cedar: compile policy: %w", err)
	}
	cp := &compiledPolicy{
		hash:          hash,
		policySet:     set,
		tenantSlug:    slug,
		perObjectEval: policyReadsPerObjectResourceAttr(set),
		expiresAt:     time.Now().Add(e.ttl),
	}
	e.compiled.Store(key, cp)
	return cp, nil
}

// PerObjectEvaluator is the optional capability an Authorizer may expose so a
// list handler can choose between one objectKey-scoped Cedar check and per-row
// checks. *Engine implements it; permissive test fakes need not — the handler
// then takes the cheap single-check path.
type PerObjectEvaluator interface {
	NeedsPerObjectEval(ctx context.Context, tenantID uuid.UUID, objectKey string) (bool, error)
}

// NeedsPerObjectEval reports whether the policies applicable to (tenantID,
// objectKey) decide on per-object resource attributes (tags, state, size, …),
// so a list handler must Cedar-check each returned object individually. It is
// false when every applicable policy is constant across the objectKey scope
// (reads only tenant_id/object_key, or no resource attributes at all), in which
// case the single up-front objectKey-scoped check already covers the whole page.
//
// Conservative by design: it does not scope the analysis by action, so a policy
// that reads a per-object attribute for ANY action turns on per-row evaluation
// for listing. That only ever costs extra Cedar calls — never a wrong decision,
// since an action-mismatched policy simply doesn't match the per-row request.
func (e *Engine) NeedsPerObjectEval(ctx context.Context, tenantID uuid.UUID, objectKey string) (bool, error) {
	cp, err := e.loadCompiled(ctx, tenantID, objectKey)
	if err != nil {
		e.m.compileErrs.Add(1)
		return false, err
	}
	return cp.perObjectEval, nil
}

// constantResourceAttrs are the Object resource attributes that do NOT vary
// across a ListObjects page — they ARE the objectKey scope. A policy reading
// only these decides identically for every object under the objectKey, so the
// single up-front check suffices. Every other resource attribute (key, state,
// size_bytes, content_type, tags, bucket_name, backend_id, and any future one)
// varies per object and forces per-row evaluation.
var constantResourceAttrs = map[string]bool{
	"tenant_id":  true,
	"object_key": true,
	// scope_keys is the Go-precomputed set of scope-strings that admit the
	// resource (see resourceScopeKeys / the scope-enforcement built-in). Within
	// a single ObjectKey scope it is CONSTANT — an ObjectKey binds to one bucket
	// under one backend in one tenant, so tenant:/backend:/bucket:/object_key:
	// are all fixed across a ListObjects page. Marking it constant keeps the
	// scope-enforcement forbid (present in every compiled set) from forcing
	// per-row Cedar evaluation on every list handler. Real tenant policies that
	// read a genuinely per-object attr (tags/state/size/…) still trigger per-row
	// eval on that attr; this entry only neutralizes the built-in's own read.
	"scope_keys": true,
}

// policyReadsPerObjectResourceAttr walks every policy in the set (via its Cedar
// JSON form) and reports whether any reads a per-object resource attribute. On a
// marshal/parse fault it returns true — the safe default is to evaluate per row
// rather than risk skipping a policy that declines individual objects.
func policyReadsPerObjectResourceAttr(set *cedar.PolicySet) bool {
	for _, p := range set.All() {
		js, err := p.MarshalJSON()
		if err != nil {
			return true // conservative: don't skip a policy we can't inspect
		}
		var tree any
		if err := json.Unmarshal(js, &tree); err != nil {
			return true
		}
		if walkReadsPerObjectResourceAttr(tree) {
			return true
		}
	}
	return false
}

// walkReadsPerObjectResourceAttr recursively scans a decoded Cedar JSON
// expression tree for an attribute access (`.`) or presence test (`has`) rooted
// directly at the `resource` variable whose attribute is not an objectKey-scope
// constant. Cedar JSON encodes `resource.tags` as
// {".": {"left": {"Var": "resource"}, "attr": "tags"}}.
func walkReadsPerObjectResourceAttr(n any) bool {
	switch v := n.(type) {
	case map[string]any:
		for _, op := range [...]string{".", "has"} {
			acc, ok := v[op].(map[string]any)
			if !ok {
				continue
			}
			if isResourceVar(acc["left"]) {
				if attr, ok := acc["attr"].(string); ok && !constantResourceAttrs[attr] {
					return true
				}
			}
		}
		for _, sub := range v {
			if walkReadsPerObjectResourceAttr(sub) {
				return true
			}
		}
	case []any:
		for _, sub := range v {
			if walkReadsPerObjectResourceAttr(sub) {
				return true
			}
		}
	}
	return false
}

func isResourceVar(n any) bool {
	m, ok := n.(map[string]any)
	if !ok {
		return false
	}
	name, ok := m["Var"].(string)
	return ok && name == "resource"
}

// builtinPolicy is concatenated with every fetched tenant/objectKey
// policy before compile. It carries the platform-admin escape hatch:
// any principal whose `roles` set contains "platform.admin" gets ALLOW
// on every action and resource. Without this, cross-tenant RPCs whose
// resource has TenantID=uuid.Nil (e.g. TenantService.ListTenants) load
// an empty policy from the store and Cedar's deny-by-default kicks in,
// surfacing as `[permission_denied] denied by policy` even for the
// bootstrap admin who's supposed to be able to do everything.
//
// Tenant-scoped policies in the store can still `forbid` specific
// actions; Cedar's first-forbid wins so an explicit ban beats this.
const builtinPolicy = `// Built-in: platform.admin gets unconditional ALLOW. Edit at your own
// risk — removing this strands a fresh cluster's bootstrap admin.
permit (
  principal,
  action,
  resource
)
when {
  principal has roles && principal.roles.contains("platform.admin")
};

// Built-in: any authenticated principal may consent to an OAuth client
// (AuthorizeOAuth) granting access to their own account — standard OAuth
// self-service. A tenant policy can still forbid it for specific principals
// or clients/scopes (first-forbid wins), e.g.
//   forbid(principal, action == Action::"AuthorizeOAuth", resource)
//   when { context.oauth_client_id == "some-client" };
permit (
  principal,
  action == Action::"AuthorizeOAuth",
  resource
);

// Built-in: any member may read their OWN tenant's record — the UI resolves
// the display name / slug for every signed-in user, so this is self-service
// like the OAuth consent above, not an admin grant. Matters especially for
// tenants with an EMPTY stored policy (the bootstrap tenant), which
// otherwise deny everything to non-platform-admins. Cross-tenant reads are
// handler-gated to platform.admin before Cedar runs, and a tenant policy
// can still forbid this (first-forbid wins).
permit (
  principal,
  action == Action::"ReadTenant",
  resource
)
when {
  principal has tenant_id &&
  resource has tenant_id &&
  principal.tenant_id == resource.tenant_id
};

// Built-in: self-service storage provisioning. A member may ensure its OWN
// tenant's storage (bucket + object-keys) via EnsureTenantStorage using a
// data-plane PAT — self-service like ReadTenant / AuthorizeOAuth above, gated
// on tenant_id equality (the handler pins the resource tenant to the caller's).
// Placing it here (not the per-tenant default template) makes it apply to ALL
// tenants — including existing ones whose stored policy was frozen at creation —
// without a per-tenant re-seed. A tenant policy can still forbid it for specific
// principals (first-forbid wins).
permit (
  principal,
  action == Action::"EnsureTenantStorage",
  resource
)
when {
  principal has tenant_id &&
  resource has tenant_id &&
  principal.tenant_id == resource.tenant_id
};

// Built-in: a MACHINE principal may delete and restore objects in its OWN
// tenant.
//
// The per-tenant default policy gates the delete family on "objectKey:admin" or
// "platform.admin", which is right for people — deletion is destructive and a
// tenant member should not do it casually — and wrong for the service that owns
// the object lifecycle. A consumer records an object, later removes the record,
// and must be able to remove the object with it; a garbage collector must be
// able to reap what nothing references. Neither can hold a role: an API token's
// principal carries none, and a capability carries none BY DESIGN (ADR-0010).
//
// Before this, the consequence was silent. consumer's avatar replacement deleted
// the previous object best-effort and swallowed the denial into a warning, so
// every replacement orphaned a file; the sweeper written to reap those orphans
// ran in dry-run and had never attempted a delete. Nothing failed loudly enough
// to be found until somebody went looking.
//
// The grant is bounded three ways: the principal must be a machine credential
// (minted deliberately — a platform admin issues an API token, a
// capability-issuer issues a capability), the object must be in that
// principal's OWN tenant, and a capability is additionally confined by its own
// caveats, which the interceptor checks before Cedar ever runs. Deletion here is
// also the soft kind: the row is marked, RestoreObject brings it back, and
// physical removal is a separate lifecycle path.
//
// Built-in rather than per-tenant so it reaches tenants whose stored policy was
// frozen at creation — the same reason EnsureTenantStorage lives here. A tenant
// that wants machines held to the role can still "forbid" it; first-forbid wins.
permit (
  principal,
  action in [Action::"DeleteObject", Action::"RestoreObject"],
  resource
)
when {
  principal has kind &&
  (principal.kind == "api_key" ||
   principal.kind == "service_account" ||
   principal.kind == "capability") &&
  principal has tenant_id &&
  resource has tenant_id &&
  principal.tenant_id == resource.tenant_id
};

// Built-in: tenant provisioning. A principal holding
// "platform.tenant-provisioner" may bring ANY tenant's storage into
// existence — the tenant row, its bucket, its object keys, and its inherited
// policy — and nothing else.
//
// Cross-tenant on purpose: the whole point is a consumer that creates an
// account and needs the matching tenant to exist without a human running a
// command. Before this, the only credential that could do it was
// platform.admin, which the built-in above grants EVERYTHING — including
// deleting any tenant and reading any object. This is the narrow slice of that
// authority which provisioning actually needs.
//
// The action list is exhaustive by intent, and the omissions are the point:
// no DeleteObject/GetObject/PutObject (no data-plane reach at all), no
// ManageBackend (it may bind to an existing backend, not create or rotate
// one), no IAM or token actions (it cannot mint a credential), and nothing
// that removes a tenant — DeleteTenant/PurgeTenant/RestoreTenant/
// RenameTenantSlug all route through ManageTenant, so the handler-side gates
// keep those on platform.admin and this permit alone cannot reach them.
//
// ManageObjectKey covers both reading and creating an object key: the
// object-key handler authorises Get with the same action as Create.
// A tenant policy can still forbid it (first-forbid wins).
permit (
  principal,
  action in [
    Action::"ManageTenant",
    Action::"ReadTenant",
    Action::"ManageBucket",
    Action::"ReadBucket",
    Action::"ManageObjectKey",
    Action::"BindObjectKeyToBucket"
  ],
  resource
)
when {
  principal has roles && principal.roles.contains("platform.tenant-provisioner")
};

// Built-in: OPT-IN resource-scope enforcement. A principal that carries a
// NON-EMPTY scopes set (and not the "*" wildcard) is confined to resources
// whose admitting scope-strings intersect its scopes. Principals with an EMPTY
// scopes set — JWT users, roles-only callers, and unscoped API tokens — are
// COMPLETELY UNAFFECTED: the "when" guard is false for them, so this forbid
// never applies. Wildcard-scoped principals ("*", reserved for platform
// admins) are exempt for the same reason.
//
// resource.scope_keys is precomputed in Go (buildEntities → resourceScopeKeys)
// as the Set<String> of every scope that admits the resource
// (tenant:/backend:/bucket:/object_key:<bucket>/<object_key>). Matching is a
// pure set-intersection here: the wire format is produced ONCE, in Go, and is
// never re-derived in Cedar. That is deliberate — Cedar has no string
// concatenation, so a policy that tried to rebuild "object_key:"+bucket+"/"+key
// would be a type error (skipped policy → fail-open). Keeping the format in Go
// makes the Go side and this policy structurally incapable of disagreeing.
//
// forbid beats permit in Cedar, so this is a hard ceiling: a scoped principal
// is denied on any out-of-scope resource even when a tenant permit would allow
// it. Empty/wildcard principals keep exactly today's behavior.
forbid (
  principal,
  action,
  resource
)
when {
  principal has scopes &&
  !principal.scopes.isEmpty() &&
  !principal.scopes.contains("*")
}
unless {
  resource has scope_keys &&
  principal.scopes.containsAny(resource.scope_keys)
};
`

// compile parses the policy text into a cedar.PolicySet, prepending the
// built-in platform-admin permit. Empty input still produces a non-empty
// set because of the builtin, which is the whole point.
func compile(text string) (*cedar.PolicySet, error) {
	combined := builtinPolicy
	if text != "" {
		combined += "\n// --- tenant policy follows ---\n" + text
	}
	return cedar.NewPolicySetFromBytes("", []byte(combined))
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

// tenantUID encodes the Tenant entity UID. Slug wins when set so that Cedar
// policies (and the resource_name format `tenants/{slug}`) read with
// human-friendly identifiers; UUID is the fallback for legacy callers and
// for tenants that have not yet been migrated to a slug.
func tenantUID(tenantID uuid.UUID, slug string) cedartypes.EntityUID {
	if slug != "" {
		return cedartypes.NewEntityUID(entityTypeTenant, cedartypes.String(slug))
	}
	return cedartypes.NewEntityUID(entityTypeTenant, cedartypes.String(tenantID.String()))
}

func objectKeyUID(tenantID uuid.UUID, objectKey string) cedartypes.EntityUID {
	// Namespace by tenant to keep objectKey IDs unique across tenants.
	return cedartypes.NewEntityUID(entityTypeObjectKey, cedartypes.String(tenantID.String()+"/"+objectKey))
}

// Canonical A-shape ObjectKey name segments (ADR-0010). Inlined here rather
// than importing internal/api/v1/object_key (that package imports cedar —
// importing it back would cycle).
const (
	cedarCanonBackendPrefix = "storageBackends/"
	cedarCanonBucketSep     = "/buckets/"
	cedarCanonTenantSep     = "/tenants/"
	cedarCanonObjectKeySep  = "/objectKeys/"
)

// objectKeyUIDFor returns the ObjectKey entity UID for the resource. When the
// canonical-EUID flag is on AND (backend, bucket) are in scope, it emits the
// canonical A-shape name; otherwise the legacy `{tenant_uuid}/{object_key}`
// form. Both keep identical entity attributes/parents, so attribute/parent
// policies are unaffected — only a hardcoded `resource == ObjectKey::"literal"`
// would see the difference (PALADIN ships none; see cedar-authoring.md §4).
func (e *Engine) objectKeyUIDFor(r *Resource) cedartypes.EntityUID {
	if e.canonicalObjectKeyEUID && r.BackendID != "" && r.BucketName != "" {
		name := cedarCanonBackendPrefix + r.BackendID +
			cedarCanonBucketSep + r.BucketName +
			cedarCanonTenantSep + r.TenantID.String() +
			cedarCanonObjectKeySep + r.ObjectKey
		return cedartypes.NewEntityUID(entityTypeObjectKey, cedartypes.String(name))
	}
	return objectKeyUID(r.TenantID, r.ObjectKey)
}

func physicalBucketUID(backendID, bucketName string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeBucket, cedartypes.String(backendID+"/"+bucketName))
}

func storageBackendUID(backendID string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeStorageBackend, cedartypes.String(backendID))
}

// resourceUID picks the most-specific entity type populated on the resource:
//   - Object         when Key/ObjectID set
//   - ObjectKey      when ObjectKey set (without Object)
//   - Bucket         when BackendID+BucketName set (without ObjectKey)
//   - StorageBackend when only BackendID set
//   - User           when TargetUserID or TargetSubject set
//   - Tenant         when only TenantID set (admin tenant ops)
func (e *Engine) resourceUID(r *Resource, authSlug string) cedartypes.EntityUID {
	if r.Key != "" || r.ObjectID != uuid.Nil {
		id := r.ObjectID.String()
		if r.ObjectID == uuid.Nil {
			id = r.ObjectKey + "/" + r.Key
		}
		return cedartypes.NewEntityUID(entityTypeObject, cedartypes.String(id))
	}
	if r.ObjectKey != "" {
		return e.objectKeyUIDFor(r)
	}
	if r.BackendID != "" && r.BucketName != "" {
		return physicalBucketUID(r.BackendID, r.BucketName)
	}
	if r.BackendID != "" {
		return storageBackendUID(r.BackendID)
	}
	if r.TargetUserID != uuid.Nil || r.TargetSubject != "" {
		return targetUserUID(r.TenantID, r.TargetUserID, r.TargetSubject)
	}
	// Tenant-as-resource: key on the DB-authoritative slug so this UID matches
	// the Tenant entity buildEntities emits (ADR-0012).
	return tenantUID(r.TenantID, authSlug)
}

// targetUserUID encodes a user-as-resource UID. The principal-User entity
// is keyed by Subject (set in userUID); this resource-User entity is keyed
// by tenant + UUID so policies can write `principal != resource` cleanly.
func targetUserUID(tenantID, userID uuid.UUID, subject string) cedartypes.EntityUID {
	id := tenantID.String() + "/"
	if userID != uuid.Nil {
		id += userID.String()
	} else {
		id += subject
	}
	return cedartypes.NewEntityUID(entityTypeUser, cedartypes.String(id))
}

func actionUID(name string) cedartypes.EntityUID {
	return cedartypes.NewEntityUID(entityTypeAction, cedartypes.String(name))
}

// resourceScopeKeys returns every scope wire-string that ADMITS resource r,
// derived from whichever identifying fields are populated. This is the single
// Go-side source of truth for the scope wire format, and it MUST stay
// byte-for-byte identical to auth.Scope.String() / auth.MatchScope
// (internal/auth/scope.go):
//
//	tenant:<tenant_uuid>
//	backend:<backend_id>
//	bucket:<bucket_name>
//	object_key:<bucket_name>/<object_key>   (requires BOTH bucket and object_key)
//
// The scope-enforcement built-in policy matches principal.scopes against the
// Set<String> this produces (exposed as resource.scope_keys) via containsAny,
// so the wire format is asserted in exactly one place. object_key deliberately
// requires the physical bucket to be known — mirroring MatchScope — so a
// resource whose bucket is not resolved at authz time simply won't carry an
// object_key/bucket key and a principal scoped by those is denied (fail-closed).
func resourceScopeKeys(r *Resource) []string {
	keys := make([]string, 0, 4)
	if r.TenantID != uuid.Nil {
		keys = append(keys, "tenant:"+r.TenantID.String())
	}
	if r.BackendID != "" {
		keys = append(keys, "backend:"+r.BackendID)
	}
	if r.BucketName != "" {
		keys = append(keys, "bucket:"+r.BucketName)
	}
	if r.ObjectKey != "" && r.BucketName != "" {
		keys = append(keys, "object_key:"+r.BucketName+"/"+r.ObjectKey)
	}
	return keys
}

// scopeKeysValue builds the Cedar Set<String> value emitted as
// resource.scope_keys on every resource-side entity.
func scopeKeysValue(r *Resource) cedartypes.Value {
	ks := resourceScopeKeys(r)
	vals := make([]cedartypes.Value, 0, len(ks))
	for _, k := range ks {
		vals = append(vals, cedartypes.String(k))
	}
	return cedartypes.NewSet(vals...)
}

// buildEntities assembles the transient entity graph passed to Authorize.
// PALADIN treats all attribute data as request-scoped — nothing is stored as
// long-lived entities in cedar-go.
//
// Entities are emitted only when the corresponding resource fields are
// populated, so admin-plane requests against a StorageBackend don't bring
// along an unrelated Tenant entity that the policy never references.
// authSlug is the DB-authoritative slug of the resource's tenant (from the
// policy fetch). It — not r.TenantSlug — keys the resource Tenant entity, and
// the User's membership anchors on the resource tenant only when the principal
// provably belongs to it (trusted-UUID equality). See ADR-0012.
func (e *Engine) buildEntities(p *Principal, r *Resource, authSlug string) cedartypes.EntityMap {
	uUID := userUID(p)
	rolesSet := make([]cedartypes.Value, 0, len(p.Roles))
	for _, role := range p.Roles {
		rolesSet = append(rolesSet, cedartypes.String(role))
	}
	scopesSet := make([]cedartypes.Value, 0, len(p.Scopes))
	for _, sc := range p.Scopes {
		scopesSet = append(scopesSet, cedartypes.String(sc))
	}

	// scopeKeys is the set of scope-strings that admit THIS resource, computed
	// once in Go and stamped as `scope_keys` on every resource-side entity below
	// (never on the principal-User). The scope-enforcement built-in matches
	// principal.scopes against it.
	scopeKeys := scopeKeysValue(r)

	m := cedartypes.EntityMap{}

	// Tenant (resource scope) — keyed on the DB-AUTHORITATIVE slug (authSlug),
	// not the request/JWT slug, so the entity graph reflects the tenant's real
	// identity. The ObjectKey/Object hierarchy parents under this entity.
	var tUID cedartypes.EntityUID
	if r.TenantID != uuid.Nil || r.TenantSlug != "" {
		tUID = tenantUID(r.TenantID, authSlug)
		m[tUID] = cedartypes.Entity{
			UID: tUID,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"tenant_id":    cedartypes.String(r.TenantID.String()),
				"slug":         cedartypes.String(authSlug),
				"display_name": cedartypes.String(""),
				"labels":       cedartypes.NewSet(),
				"scope_keys":   scopeKeys,
			}),
		}
	}

	// User membership — anchored on the PRINCIPAL's tenant, gated by
	// trusted-UUID equality (ADR-0012). The principal is placed under the
	// resource's Tenant entity ONLY when p.TenantID == r.TenantID (both trusted
	// UUIDs), so `principal in Tenant::"<slug>"` means "this caller really
	// belongs to this tenant" — making Cedar an independent second isolation
	// layer, not a rubber stamp for whatever tenant the resource is in. When
	// the tenants differ (a cross-tenant admin, or a call that slipped past
	// assertJWTTenant), the User is anchored under the principal's own
	// UUID-keyed Tenant, which cannot match a slug-keyed member permit → deny
	// (role permits like platform.admin are unaffected — they don't key on
	// membership).
	userParents := cedartypes.EntityUIDSet{}
	if p.TenantID != uuid.Nil || p.TenantSlug != "" {
		var pTUID cedartypes.EntityUID
		if r.TenantID != uuid.Nil && p.TenantID == r.TenantID {
			pTUID = tUID // same tenant → the DB-authoritative resource Tenant entity
		} else {
			// Different (or no) resource tenant → anchor under the principal's
			// own UUID-keyed Tenant. Never the JWT slug (attacker-controlled).
			pTUID = tenantUID(p.TenantID, "")
			if _, ok := m[pTUID]; !ok {
				m[pTUID] = cedartypes.Entity{
					UID: pTUID,
					Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
						"tenant_id":    cedartypes.String(p.TenantID.String()),
						"slug":         cedartypes.String(""),
						"display_name": cedartypes.String(""),
						"labels":       cedartypes.NewSet(),
					}),
				}
			}
		}
		userParents = cedartypes.NewEntityUIDSet(pTUID)
	}
	m[uUID] = cedartypes.Entity{
		UID:     uUID,
		Parents: userParents,
		Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
			"subject":     cedartypes.String(p.Subject),
			"tenant_id":   cedartypes.String(p.TenantID.String()),
			"tenant_slug": cedartypes.String(p.TenantSlug),
			// Always present, empty when unknown: a policy that reads
			// `principal.kind` must not hit an evaluation error (Cedar fails
			// closed on those, denying a legitimate call for the wrong reason).
			"kind":   cedartypes.String(p.Kind),
			"roles":  cedartypes.NewSet(rolesSet...),
			"scopes": cedartypes.NewSet(scopesSet...),
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
				"scope_keys": scopeKeys,
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
				"scope_keys":      scopeKeys,
			}),
		}
		m[bUID] = bucketEntity
	}

	// ObjectKey — child of Tenant (and Bucket when bucket is in scope).
	var okUID cedartypes.EntityUID
	if r.ObjectKey != "" && r.TenantID != uuid.Nil {
		okUID = e.objectKeyUIDFor(r)
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
				"scope_keys":  scopeKeys,
			}),
		}
	}

	// Object — child of ObjectKey.
	if r.Key != "" || r.ObjectID != uuid.Nil {
		oUID := e.resourceUID(r, authSlug)
		// tags: Set<String> of KEYS (membership tests, back-compat).
		// tag_values: Record<String,String> of key→value, so policies can test
		// a value, e.g. `resource.tag_values has "classified" &&
		// resource.tag_values["classified"] == "true"`. Both are populated from
		// the same map; keeping `tags` avoids breaking existing key-membership
		// policies.
		tagsSet := make([]cedartypes.Value, 0, len(r.Tags))
		tagValues := make(cedartypes.RecordMap, len(r.Tags))
		for k, v := range r.Tags {
			tagsSet = append(tagsSet, cedartypes.String(k))
			tagValues[cedartypes.String(k)] = cedartypes.String(v)
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
				"tag_values":   cedartypes.NewRecord(tagValues),
				"scope_keys":   scopeKeys,
			}),
		}
	}

	// User-as-resource — distinct UID family from the principal-User. Policies
	// can write rules about managing other users; principal != resource
	// because Subjects and (tenant_id/user_id) keys never collide.
	if r.TargetUserID != uuid.Nil || r.TargetSubject != "" {
		uResUID := targetUserUID(r.TenantID, r.TargetUserID, r.TargetSubject)
		var parents cedartypes.EntityUIDSet
		if r.TenantID != uuid.Nil {
			parents = cedartypes.NewEntityUIDSet(tUID)
		}
		m[uResUID] = cedartypes.Entity{
			UID:     uResUID,
			Parents: parents,
			Attributes: cedartypes.NewRecord(cedartypes.RecordMap{
				"user_id":    cedartypes.String(r.TargetUserID.String()),
				"subject":    cedartypes.String(r.TargetSubject),
				"tenant_id":  cedartypes.String(r.TenantID.String()),
				"scope_keys": scopeKeys,
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
	scopes := make([]cedartypes.Value, 0, len(rc.OAuthScopes))
	for _, s := range rc.OAuthScopes {
		scopes = append(scopes, cedartypes.String(s))
	}
	return cedartypes.NewRecord(cedartypes.RecordMap{
		"size_bytes":      cedartypes.Long(rc.SizeBytes),
		"content_type":    cedartypes.String(rc.ContentType),
		"now":             cedartypes.Long(now.Unix()),
		"ip":              cedartypes.String(rc.IP),
		"oauth_client_id": cedartypes.String(rc.OAuthClientID),
		"oauth_scopes":    cedartypes.NewSet(scopes...),
	})
}

// metrics reserves counters the production wire-up should export via OTEL.
type metrics struct {
	authzAllowed atomic.Uint64
	authzDenied  atomic.Uint64
	compileErrs  atomic.Uint64
	// evalErrs counts authorize calls where ≥1 policy failed to evaluate (and
	// was skipped) — the request was denied fail-closed. Non-zero means a
	// policy is broken and some requests are being denied for the wrong reason.
	evalErrs    atomic.Uint64
	cacheHits   atomic.Uint64
	cacheMisses atomic.Uint64
	// watchResyncs counts full-cache flushes triggered by a Store reconnect
	// (ResyncAll). Non-zero means the LISTEN link dropped at least once and
	// the cache was conservatively cleared.
	watchResyncs atomic.Uint64
}

// Stats returns a snapshot of the internal metrics.
func (e *Engine) Stats() (allowed, denied, compileErrs, hits, misses uint64) {
	return e.m.authzAllowed.Load(), e.m.authzDenied.Load(), e.m.compileErrs.Load(), e.m.cacheHits.Load(), e.m.cacheMisses.Load()
}

// WatchResyncs returns how many times the invalidation loop flushed the whole
// compiled cache in response to a Store reconnect. Exposed separately from
// Stats so a health probe can alarm on a flapping LISTEN link.
func (e *Engine) WatchResyncs() uint64 { return e.m.watchResyncs.Load() }

// EvalErrs returns how many authorize calls hit a policy-evaluation error and
// were denied fail-closed. Exposed separately from Stats so a health probe can
// alarm on a broken policy. Non-zero = a policy is faulting at eval time.
func (e *Engine) EvalErrs() uint64 { return e.m.evalErrs.Load() }
