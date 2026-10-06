// Package policy exposes policy-text helpers (validation, simulation,
// effective-policy lookup) to the API layer. Persistence still lives on
// TenantService / CollectionService via their inherited_cedar_policy and
// CollectionPolicy.cedar_policy fields.
package policyh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/policies"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Handler offers Cedar inspection helpers — validation, dry-run authz, and
// effective-policy assembly. ValidatePolicy is purely textual; the rest
// require a live Cedar engine + Store.
type Handler struct {
	engine cedar.Authorizer
	store  cedar.Store
}

// NewHandler constructs a handler. engine and store are required.
func NewHandler(engine cedar.Authorizer, store cedar.Store) *Handler {
	if engine == nil || store == nil {
		panic("policy: engine and store are required")
	}
	return &Handler{engine: engine, store: store}
}

// authorizeInspect gates a policy-introspection RPC against Cedar. Resource
// carries tenant + collection when known; ValidatePolicy passes empty
// Resource and relies on policies that match by principal role.
func (h *Handler) authorizeInspect(ctx context.Context, tenantID uuid.UUID, collection string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.engine.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		cedar.ActionInspectPolicy,
		&cedar.Resource{TenantID: tenantID, Collection: collection},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// Diagnostic severities, as PolicyDiagnostic.severity spells them.
const (
	DiagnosticError   = "error"
	DiagnosticWarning = "warning"
)

// Diagnostic is one finding about a policy.
type Diagnostic struct {
	Severity string
	Message  string
}

// ValidateOutput is ValidatePolicy's result. OK is false only when the text
// does not compile.
type ValidateOutput struct {
	OK          bool
	Diagnostics []Diagnostic
}

// validateName labels schema findings; the console shows them under the
// editor, so there is no file to name.
const validateName = "policy"

// ValidatePolicy compiles the Cedar policy text and type-checks it against
// policies/schema.cedarschema. A compile failure is an error and OK=false.
// A schema finding — an action that does not exist, an attribute the entity
// lacks, a read that needs `has` — is a warning: the text still compiles and
// would be stored, but the read it names fails at request time and the engine
// denies. Warnings rather than errors because the schema cannot declare
// everything the engine provides (Object.tag_values). A protocol error is
// returned only when the caller is unauthorized.
func (h *Handler) ValidatePolicy(ctx context.Context, text string) (*ValidateOutput, error) {
	if err := h.authorizeInspect(ctx, uuid.Nil, ""); err != nil {
		return nil, err
	}
	if err := cedar.Validate(text); err != nil {
		return &ValidateOutput{Diagnostics: []Diagnostic{{Severity: DiagnosticError, Message: err.Error()}}}, nil
	}
	findings, err := policies.Check(validateName, []byte(text))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("schema check: %w", err))
	}
	out := &ValidateOutput{OK: true}
	for _, f := range findings {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Severity: DiagnosticWarning, Message: f})
	}
	return out, nil
}

// ─── SimulateAuthz ──────────────────────────────────────────────────────────

// SimulateAuthzInput is the shape consumed by SimulateAuthz. ResourceName is
// the canonical resource name (e.g. tenants/{t}/collections/{ok}) — handler
// derives tenant + collection for the engine call.
type SimulateAuthzInput struct {
	PrincipalSubject  string
	PrincipalTenantID uuid.UUID
	PrincipalRoles    []string
	// PrincipalKind is the credential type the simulated caller would carry.
	PrincipalKind string
	Action        string
	ResourceName  string
}

type SimulateAuthzOutput struct {
	Allowed     bool
	Explanation string
}

// SimulateAuthz answers "would this principal be allowed?" without performing
// the action. Used by admin UI access pre-flight and the MCP bridge.
func (h *Handler) SimulateAuthz(ctx context.Context, in SimulateAuthzInput) (*SimulateAuthzOutput, error) {
	tenantID, collection, err := parseSimulateResource(in.ResourceName, in.PrincipalTenantID)
	if err != nil {
		return nil, err
	}
	if err := h.authorizeInspect(ctx, tenantID, collection); err != nil {
		return nil, err
	}
	res := &cedar.Resource{
		TenantID:   tenantID,
		Collection: collection,
	}
	// A hypothetical principal, built from the request rather than from a
	// credential — the one place a literal is right, because there IS no
	// auth.Principal to lift.
	princ := &cedar.Principal{
		Subject:  in.PrincipalSubject,
		TenantID: tenantID,
		Kind:     in.PrincipalKind,
		Roles:    in.PrincipalRoles,
	}
	// The action comes from the request, so it is resolved against the declared
	// set: an unknown name is a malformed question, not a "no" — answering Deny
	// would tell the caller its policy refuses something no policy can name.
	action, ok := cedar.LookupAction(in.Action)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("action %q is not declared in the schema", in.Action))
	}
	decision, err := h.engine.IsAuthorized(ctx, princ, action, res, cedar.RequestContext{Now: time.Now()})
	if err != nil {
		return nil, fmt.Errorf("authz: %w", err)
	}
	return &SimulateAuthzOutput{
		Allowed:     decision == cedar.DecisionAllow,
		Explanation: fmt.Sprintf("simulated: %s on %s → %v", in.Action, in.ResourceName, decision),
	}, nil
}

// parseSimulateResource extracts tenantID + collection from a v2 resource
// name. Falls back to the principal's tenant when the resource name does
// not embed one (e.g. backend names).
func parseSimulateResource(name string, fallbackTenant uuid.UUID) (uuid.UUID, string, error) {
	// A collection's name may contain '/', so the collection is everything
	// between "collections/" and an object's "/objects/{id}" — the SDK's
	// parsers draw that line, as the data plane's do. Taking the next path
	// segment read "e2e/logs" as "e2e", another collection's policy.
	if o, err := paladin.ParseObjectName(name); err == nil {
		return collectionScope(o.CollectionName)
	}
	if c, err := paladin.ParseCollectionName(name); err == nil {
		return collectionScope(c)
	}
	parts := strings.Split(name, "/")
	if len(parts) >= 4 && parts[0] == "tenants" && parts[2] == "collections" {
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("invalid tenant id in %q: %w", name, err)
		}
		return id, parts[3], nil
	}
	if len(parts) >= 2 && parts[0] == "tenants" {
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("invalid tenant id in %q: %w", name, err)
		}
		return id, "", nil
	}
	// Backend / bucket names — Cedar only sees the tenant-level policy.
	return fallbackTenant, "", nil
}

func collectionScope(n paladin.CollectionName) (uuid.UUID, string, error) {
	id, err := uuid.Parse(n.Tenant)
	return id, n.Collection, err
}

// ─── GetEffectivePolicy ─────────────────────────────────────────────────────

type EffectivePolicyOutput struct {
	MergedCedarPolicy string
	Layers            []PolicyLayer
}

type PolicyLayer struct {
	Source      string
	CedarPolicy string
	// Frozen: CedarPolicy does not compile, and EvaluatedCedarPolicy — a
	// freeze over the layer's scope — is evaluated in its place.
	Frozen               bool
	EvaluatedCedarPolicy string
}

// BuiltinLayerSource names the platform's own layer, which no resource owns.
const BuiltinLayerSource = "built-in"

// GetEffectivePolicy returns the merged Cedar text the engine would compile
// for the given resource, plus its layer breakdown for inspection.
func (h *Handler) GetEffectivePolicy(ctx context.Context, resourceName string, fallbackTenant uuid.UUID) (*EffectivePolicyOutput, error) {
	tenantID, collection, err := parseSimulateResource(resourceName, fallbackTenant)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.authorizeInspect(ctx, tenantID, collection); err != nil {
		return nil, err
	}
	// Read as the tenant asked about, as the authorizer reads it for that
	// tenant's requests. The caller's own scope hides another tenant's
	// collection from RLS, and with it the collection's policy and its
	// bucket's — an admin inspecting tenant X saw neither.
	stored, _, _, err := h.store.Fetch(auth.WithActingTenant(ctx, tenantID), tenantID, collection)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("fetch policy: %w", err))
	}
	// Built by the engine's own join, so what this returns is what the
	// authorizer compiles — the built-in layer and any freeze included.
	layers, text := cedar.EvaluatedLayers(stored)
	builtin := cedar.BuiltinPolicy()
	out := &EffectivePolicyOutput{
		MergedCedarPolicy: cedar.CompiledText(text),
		Layers: []PolicyLayer{{
			Source: BuiltinLayerSource, CedarPolicy: builtin, EvaluatedCedarPolicy: builtin,
		}},
	}
	tenantName := apiutil.TenantNamePrefix + tenantID.String()
	// Every layer the resource is subject to is listed, an empty one
	// included: a collection with no policy of its own, in a bucket with
	// none, still shows both — that they add nothing is the answer. The
	// engine leaves empty layers out of the text, not out of scope.
	scope := []struct{ name, source string }{{cedar.LayerTenant, tenantName}}
	if stored.BucketName != "" {
		scope = append(scope, struct{ name, source string }{cedar.LayerBucket, stored.BucketName})
	}
	if collection != "" {
		scope = append(scope, struct{ name, source string }{cedar.LayerCollection, tenantName + collectionsSegment + collection})
	}
	evaluated := map[string]cedar.EvaluatedLayer{}
	for _, l := range layers {
		evaluated[l.Name] = l
	}
	for _, sc := range scope {
		l := evaluated[sc.name]
		out.Layers = append(out.Layers, PolicyLayer{
			Source:               sc.source,
			CedarPolicy:          l.Stored,
			Frozen:               l.Frozen,
			EvaluatedCedarPolicy: l.Evaluated,
		})
	}
	return out, nil
}

// collectionsSegment joins a tenant's name to one of its collections.
const collectionsSegment = "/collections/"
