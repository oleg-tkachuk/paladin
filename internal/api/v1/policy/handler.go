// Package policy exposes policy-text helpers (validation, simulation,
// effective-policy lookup) to the API layer. Persistence still lives on
// TenantService / ObjectKeyService via their inherited_cedar_policy and
// ObjectKeyPolicy.cedar_policy fields.
package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// Handler offers Cedar inspection helpers — validation, dry-run authz, and
// effective-policy assembly. The engine + store are required for the latter
// two; ValidatePolicy is purely textual.
type Handler struct {
	engine *cedar.Engine
	store  cedar.Store
}

// NewHandler constructs a handler. engine and store may be nil — those code
// paths return Unimplemented at the connectshim layer.
func NewHandler(engine *cedar.Engine, store cedar.Store) *Handler {
	return &Handler{engine: engine, store: store}
}

// ValidatePolicy parses the Cedar policy text. Returns (valid, parser-error-text).
func (h *Handler) ValidatePolicy(_ context.Context, text string) (bool, string) {
	if err := cedar.Validate(text); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// ─── SimulateAuthz ──────────────────────────────────────────────────────────

// SimulateAuthzInput is the shape consumed by SimulateAuthz. ResourceName is
// the canonical resource name (e.g. tenants/{t}/objectKeys/{ok}) — handler
// derives tenant + object_key for the engine call.
type SimulateAuthzInput struct {
	PrincipalSubject  string
	PrincipalTenantID uuid.UUID
	PrincipalRoles    []string
	Action            string
	ResourceName      string
}

type SimulateAuthzOutput struct {
	Allowed     bool
	Explanation string
}

// SimulateAuthz answers "would this principal be allowed?" without performing
// the action. Used by admin UI access pre-flight and the MCP bridge.
func (h *Handler) SimulateAuthz(ctx context.Context, in SimulateAuthzInput) (*SimulateAuthzOutput, error) {
	if h.engine == nil {
		return nil, errors.New("policy: engine not wired")
	}
	tenantID, objectKey, err := parseSimulateResource(in.ResourceName, in.PrincipalTenantID)
	if err != nil {
		return nil, err
	}
	res := &cedar.Resource{
		TenantID:  tenantID,
		ObjectKey: objectKey,
	}
	princ := &cedar.Principal{
		Subject:  in.PrincipalSubject,
		TenantID: tenantID,
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

// parseSimulateResource extracts tenantID + objectKey from a v2 resource
// name. Falls back to the principal's tenant when the resource name does
// not embed one (e.g. backend names).
func parseSimulateResource(name string, fallbackTenant uuid.UUID) (uuid.UUID, string, error) {
	parts := strings.Split(name, "/")
	if len(parts) >= 4 && parts[0] == "tenants" && parts[2] == "objectKeys" {
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
	if h.store == nil {
		return nil, errors.New("policy: store not wired")
	}
	tenantID, objectKey, err := parseSimulateResource(resourceName, fallbackTenant)
	if err != nil {
		return nil, err
	}
	merged, _, err := h.store.Fetch(ctx, tenantID, objectKey)
	if err != nil {
		return nil, err
	}
	out := &EffectivePolicyOutput{MergedCedarPolicy: merged}
	// Stable layer attribution: tenant first, then object_key. We do not
	// surface bucket-layer policy until BucketRepository.GetPolicy is wired
	// to PostgresStore (slice 7).
	out.Layers = append(out.Layers, PolicyLayer{
		Source:      fmt.Sprintf("tenants/%s", tenantID),
		CedarPolicy: extractTenantLayer(merged),
	})
	if objectKey != "" {
		out.Layers = append(out.Layers, PolicyLayer{
			Source:      fmt.Sprintf("tenants/%s/objectKeys/%s", tenantID, objectKey),
			CedarPolicy: extractObjectKeyLayer(merged),
		})
	}
	return out, nil
}

const objectKeyLayerMarker = "// --- objectKey-scoped ---\n"

func extractTenantLayer(merged string) string {
	if i := strings.Index(merged, objectKeyLayerMarker); i >= 0 {
		return strings.TrimRight(merged[:i], "\n")
	}
	return merged
}

func extractObjectKeyLayer(merged string) string {
	if i := strings.Index(merged, objectKeyLayerMarker); i >= 0 {
		return merged[i+len(objectKeyLayerMarker):]
	}
	return ""
}
