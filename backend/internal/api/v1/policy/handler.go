// Package policy exposes policy-text helpers (validation, simulation,
// effective-policy lookup) to the API layer. Persistence still lives on
// TenantService / CollectionService via their inherited_cedar_policy and
// CollectionPolicy.cedar_policy fields.
package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
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
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// ValidatePolicy parses the Cedar policy text. Returns (valid, parser-error-text)
// for compile failures. A protocol error is returned only when the caller
// is unauthorized — once past authz, parser issues come back through the
// (bool, string) result.
func (h *Handler) ValidatePolicy(ctx context.Context, text string) (bool, string, error) {
	if err := h.authorizeInspect(ctx, uuid.Nil, ""); err != nil {
		return false, "", err
	}
	if err := cedar.Validate(text); err != nil {
		return false, err.Error(), nil
	}
	return true, "", nil
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
	decision, err := h.engine.IsAuthorized(ctx, princ, in.Action, res, cedar.RequestContext{Now: time.Now()})
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

// ─── GetEffectivePolicy ─────────────────────────────────────────────────────

type EffectivePolicyOutput struct {
	MergedCedarPolicy string
	Layers            []PolicyLayer
}

type PolicyLayer struct {
	Source      string
	CedarPolicy string
}

// GetEffectivePolicy returns the merged Cedar text the engine would compile
// for the given resource, plus its layer breakdown for inspection.
func (h *Handler) GetEffectivePolicy(ctx context.Context, resourceName string, fallbackTenant uuid.UUID) (*EffectivePolicyOutput, error) {
	tenantID, collection, err := parseSimulateResource(resourceName, fallbackTenant)
	if err != nil {
		return nil, err
	}
	if err := h.authorizeInspect(ctx, tenantID, collection); err != nil {
		return nil, err
	}
	merged, _, _, err := h.store.Fetch(ctx, tenantID, collection)
	if err != nil {
		return nil, err
	}
	out := &EffectivePolicyOutput{MergedCedarPolicy: merged}
	// Stable layer attribution: tenant first, then collection. We do not
	// surface bucket-layer policy until BucketRepository.GetPolicy is wired
	// to PostgresStore (slice 7).
	out.Layers = append(out.Layers, PolicyLayer{
		Source:      fmt.Sprintf("tenants/%s", tenantID),
		CedarPolicy: extractTenantLayer(merged),
	})
	if collection != "" {
		out.Layers = append(out.Layers, PolicyLayer{
			Source:      fmt.Sprintf("tenants/%s/collections/%s", tenantID, collection),
			CedarPolicy: extractCollectionLayer(merged),
		})
	}
	return out, nil
}

const collectionLayerMarker = "// --- collection-scoped ---\n"

func extractTenantLayer(merged string) string {
	if i := strings.Index(merged, collectionLayerMarker); i >= 0 {
		return strings.TrimRight(merged[:i], "\n")
	}
	return merged
}

func extractCollectionLayer(merged string) string {
	if i := strings.Index(merged, collectionLayerMarker); i >= 0 {
		return merged[i+len(collectionLayerMarker):]
	}
	return ""
}
