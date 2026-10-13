package postgres

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// The JSON a capability record keeps of its principal, its issuing principal
// and its caveats (capability_records.principal_payload, issued_by, caveats).
// limes's types carry no json tags: their field names are Go's to change, and
// a renamed field would silently change what a row says. These structs are
// the stored form, spelled as the token spells the same claims, so a row
// reads the same as the token it was issued as and migration 058's rows read
// as they were written.

// storedPrincipal is a limes.Principal as a row holds it.
type storedPrincipal struct {
	Type     limes.PrincipalType `json:"type"`
	TenantID uuid.UUID           `json:"tenant_id"`
	Subject  string              `json:"subject"`
	Agent    *storedAgent        `json:"agent,omitzero"`
}

// storedAgent is a limes.AgentPrincipal as a row holds it, field for field;
// a test holds the two to the same fields.
type storedAgent struct {
	AgentType     string    `json:"agent_type,omitempty"`
	AgentVersion  string    `json:"agent_version,omitempty"`
	RunID         uuid.UUID `json:"run_id,omitzero"`
	ParentAgentID uuid.UUID `json:"parent_agent_id,omitzero"`
	Model         string    `json:"model,omitempty"`
	MCPClient     string    `json:"mcp_client,omitempty"`
}

// storedCaveats is a limes.Caveats as a row holds it, field for field but for
// the ceilings; a test holds the two to the same fields. Each ceiling is two
// members, as in the token: an absent max is a ceiling of 0, and only the
// unlimited flag lifts it (066 rewrote the rows that read 0 as unlimited).
type storedCaveats struct {
	Ops                    []limes.Op  `json:"ops"`
	ResourcePrefixes       []string    `json:"resource_prefixes,omitempty"`
	ResourceURIs           []string    `json:"resource_uris,omitempty"`
	MaxRequests            int64       `json:"max_requests,omitzero"`
	UnlimitedRequests      bool        `json:"unlimited_requests,omitzero"`
	MaxBudget              limes.Nanos `json:"max_budget,omitzero"`
	UnlimitedBudget        bool        `json:"unlimited_budget,omitzero"`
	UnitCode               string      `json:"unit_code,omitempty"`
	AllowTaintedRead       bool        `json:"allow_tainted_read,omitzero"`
	IdempotencyKeyRequired bool        `json:"idempotency_key_required,omitzero"`
	SourceIPCIDR           []string    `json:"source_ip_cidr,omitempty"`
}

func toStoredPrincipal(p limes.Principal) storedPrincipal {
	s := storedPrincipal{Type: p.Type, TenantID: p.TenantID, Subject: p.Subject}
	if a := p.Agent; a != nil {
		s.Agent = &storedAgent{
			AgentType: a.AgentType, AgentVersion: a.AgentVersion, RunID: a.RunID,
			ParentAgentID: a.ParentAgentID, Model: a.Model, MCPClient: a.MCPClient,
		}
	}
	return s
}

func (s storedPrincipal) principal() limes.Principal {
	p := limes.Principal{Type: s.Type, TenantID: s.TenantID, Subject: s.Subject}
	if a := s.Agent; a != nil {
		p.Agent = &limes.AgentPrincipal{
			AgentType: a.AgentType, AgentVersion: a.AgentVersion, RunID: a.RunID,
			ParentAgentID: a.ParentAgentID, Model: a.Model, MCPClient: a.MCPClient,
		}
	}
	return p
}

func toStoredCaveats(c limes.Caveats) storedCaveats {
	return storedCaveats{
		Ops: c.Ops, ResourcePrefixes: c.ResourcePrefixes, ResourceURIs: c.ResourceURIs,
		MaxRequests: c.Requests.Max, UnlimitedRequests: c.Requests.Unlimited,
		MaxBudget: c.Budget.Max, UnlimitedBudget: c.Budget.Unlimited,
		UnitCode: c.UnitCode, AllowTaintedRead: c.AllowTaintedRead,
		IdempotencyKeyRequired: c.IdempotencyKeyRequired, SourceIPCIDR: c.SourceIPCIDR,
	}
}

func (s storedCaveats) caveats() limes.Caveats {
	return limes.Caveats{
		Ops: s.Ops, ResourcePrefixes: s.ResourcePrefixes, ResourceURIs: s.ResourceURIs,
		Requests: limes.RequestCeiling{Max: s.MaxRequests, Unlimited: s.UnlimitedRequests},
		Budget:   limes.BudgetCeiling{Max: s.MaxBudget, Unlimited: s.UnlimitedBudget},
		UnitCode: s.UnitCode, AllowTaintedRead: s.AllowTaintedRead,
		IdempotencyKeyRequired: s.IdempotencyKeyRequired, SourceIPCIDR: s.SourceIPCIDR,
	}
}

// marshalPrincipal is p as a row's JSON.
func marshalPrincipal(p limes.Principal) ([]byte, error) {
	raw, err := json.Marshal(toStoredPrincipal(p))
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: marshal principal: %w", err)
	}
	return raw, nil
}

// unmarshalPrincipal reads a row's principal JSON.
func unmarshalPrincipal(raw []byte) (limes.Principal, error) {
	var s storedPrincipal
	if err := json.Unmarshal(raw, &s); err != nil {
		return limes.Principal{}, fmt.Errorf("capability/postgres: parse principal: %w", err)
	}
	return s.principal(), nil
}

// marshalCaveats is c as a row's JSON.
func marshalCaveats(c limes.Caveats) ([]byte, error) {
	raw, err := json.Marshal(toStoredCaveats(c))
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: marshal caveats: %w", err)
	}
	return raw, nil
}

// unmarshalCaveats reads a row's caveats JSON.
func unmarshalCaveats(raw []byte) (limes.Caveats, error) {
	var s storedCaveats
	if err := json.Unmarshal(raw, &s); err != nil {
		return limes.Caveats{}, fmt.Errorf("capability/postgres: parse caveats: %w", err)
	}
	return s.caveats(), nil
}
