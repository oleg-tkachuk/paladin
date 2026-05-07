// Package capability is PALADIN's object-capability authorisation primitive,
// designed for agentic workloads.
//
// A Capability is a short-lived, signed token that grants its bearer a
// specific set of operations on a specific set of resources, with
// explicit budget and lifetime caveats. It is delegable (a parent issues
// a strictly-narrower child to a sub-agent) and individually revocable.
// Compared to long-lived API keys or role-based RBAC, capabilities give:
//
//   - Per-tool-call attribution: every Connect / MCP call carries the
//     capability ID; audit and cost dashboards roll up by it.
//
//   - Budget enforcement at the auth layer: the verifier rejects calls
//     once the per-capability USD budget is exhausted, rather than the
//     business logic discovering it three RPCs deep.
//
//   - Mid-flight revocation: a compromised agent's capability is revoked
//     atomically; the cache propagates within the configured TTL (≤2s
//     default), and pending writes are fenced by `Generation`.
//
//   - Sub-capability for sub-agents: an orchestrator agent can issue a
//     narrower capability to each tool agent it spawns, without going
//     back through an admin API.
//
// Wire format is a JWT compact serialization with Ed25519 signatures
// (alg=EdDSA). Public keys are surfaced via a JWKS endpoint so consumers
// (data-plane interceptor, MCP server) verify locally without an extra
// RPC.
//
// This package is currently the foundation only — the issuer / verifier
// / Postgres store wiring lands in follow-up commits. Today: types,
// caveats, signer/verifier interfaces, and revocation cache. Migration
// 016 ships the schema. RPC surface (CapabilityService) is BACKLOG.
package capability

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Errors exposed to callers. Mapped to MCP / Connect errors at the seam
// (interceptor) — not here. Verifier returns these so callers can switch.
var (
	ErrInvalidSignature  = errors.New("capability: invalid signature")
	ErrExpired           = errors.New("capability: expired")
	ErrNotYetValid       = errors.New("capability: not yet valid")
	ErrRevoked           = errors.New("capability: revoked")
	ErrCaveatViolation   = errors.New("capability: caveat violation")
	ErrAudienceMismatch  = errors.New("capability: audience mismatch")
	ErrBudgetExceeded    = errors.New("capability: budget exceeded")
	ErrDelegationTooWide = errors.New("capability: child wider than parent")
)

// Op is a coarse-grained operation an agent may perform. The set is
// intentionally small — fine-grained method gating goes through Cedar
// (admin-authored policy) on top of these. Capabilities are the
// authentication-and-coarse-authorisation primitive; Cedar is the
// fine-grained admin-policy primitive.
type Op string

const (
	OpGet     Op = "get"
	OpPut     Op = "put"
	OpList    Op = "list"
	OpDelete  Op = "delete"
	OpPresign Op = "presign"
	OpTag     Op = "tag"
	OpSearch  Op = "search"
	OpEmbed   Op = "embed"
	OpShare   Op = "share"  // delegate a sub-capability to another agent
	OpManage  Op = "manage" // admin-tier (tenant CRUD, policy authoring)
)

// Principal identifies the subject the capability is issued to. Agent
// principals have a richer shape than service principals: they carry
// the run / tool-call lineage that audit and cost attribution need.
type Principal struct {
	// Type discriminates the principal kind.
	Type PrincipalType
	// TenantID is required for every principal — capability without
	// tenant scope is malformed and the verifier rejects it.
	TenantID uuid.UUID
	// Subject is the stable identity (user UUID, agent run ID, service
	// account name). Format depends on Type.
	Subject string
	// Agent fields are populated only for Type == PrincipalAgent.
	Agent *AgentPrincipal
}

// PrincipalType discriminates Principal.Type.
type PrincipalType string

const (
	PrincipalUser    PrincipalType = "user"
	PrincipalAgent   PrincipalType = "agent"
	PrincipalService PrincipalType = "service"
)

// AgentPrincipal carries the fields specific to AI-agent principals.
// Threaded into audit + lineage so per-tool-call replay reconstructs
// the call graph without joining across half a dozen tables.
type AgentPrincipal struct {
	// AgentType is a human-meaningful label ("research-orchestrator",
	// "code-reviewer-bot"). Used for grouping, dashboards, anomaly
	// baselines.
	AgentType string
	// AgentVersion pins the build / git-sha of the agent runtime.
	AgentVersion string
	// RunID is a UUID minted by the agent host at session start.
	RunID uuid.UUID
	// ParentAgentID is set when this agent was spawned by another via
	// `OpShare`. Empty for top-level agents.
	ParentAgentID uuid.UUID
	// Model identifies the LLM behind the agent ("claude-opus-4-7",
	// "gpt-5-pro"). Used for cost attribution.
	Model string
	// MCPClient is the agent host's user-agent ("claude-desktop@1.4.0",
	// "cursor@0.44"). Useful for compatibility tracking.
	MCPClient string
}

// Capability is the in-memory representation of a verified token. The
// wire form is a JWT; this struct is what callers receive from
// Verifier.Verify and what the Issuer fills before signing.
type Capability struct {
	// ID is the unique identifier (UUIDv7 — sortable by issuance time
	// so per-tenant indexes stay tight).
	ID uuid.UUID

	// Issuer is the PALADIN instance that minted the capability. Verifiers
	// accept tokens whose Issuer is in the trusted set.
	Issuer string

	// Subject is the principal the capability authorises.
	Subject Principal

	// Audience pins which PALADIN planes the capability is valid for.
	// Typical values: "data", "admin", "mcp". Verifiers reject tokens
	// whose audience does not include the calling plane.
	Audience []string

	// Caveats is the conjunction of restrictions — every caveat must
	// be satisfied by the request, and child capabilities can only
	// add further caveats, never relax one.
	Caveats Caveats

	// IssuedAt / NotBefore / ExpiresAt are standard JWT-style times.
	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time

	// ParentID, if non-zero, points at the parent capability whose
	// holder delegated this one. The verifier walks the chain on
	// every request to validate caveat narrowing.
	ParentID uuid.UUID

	// Generation is a monotone counter the issuer increments whenever
	// it rotates the capability identity (e.g. soft-revoke + reissue
	// during a credential rotation). Worker writes thread it through
	// fence-token checks so a stale capability cannot mutate state
	// after a rotation.
	Generation int64
}

// Caveats is a typed bag of restrictions. Empty values are interpreted
// as "no restriction on that axis"; explicit bounds are AND-combined.
//
// A child capability may only narrow caveats — set a tighter prefix,
// drop ops, lower the budget, shorten TTL. The verifier enforces this
// at delegation time, not just at use time, so attempts to widen are
// detected at issue and rejected.
type Caveats struct {
	// Ops is the set of operations the capability authorises. Empty =
	// no operation allowed (a capability with no ops is meaningless;
	// the issuer rejects it). Subsets can be expressed as multiple Ops
	// joined by repetition.
	Ops []Op

	// ResourcePrefixes restricts the capability to URIs starting with
	// any prefix in the slice. Empty = unrestricted within the tenant.
	// The prefix is matched as a string operation; backends translate
	// it into native filters.
	ResourcePrefixes []string

	// ResourceURIs pins the capability to specific URIs. Used by
	// hand-off flows where the parent passes exactly the artifacts the
	// child needs.
	ResourceURIs []string

	// MaxRequests caps the total number of times the capability may
	// be used. 0 = unlimited within the TTL.
	MaxRequests int

	// MaxBudgetUSD is the cost budget the capability authorises. The
	// budget tracker decrements as LLM / storage operations bill; once
	// exhausted, the verifier returns ErrBudgetExceeded.
	MaxBudgetUSD float64

	// AllowTaintedRead permits read of objects flagged with prompt-
	// injection / PII / secrets signals. Off by default — a deliberate
	// elicit-and-confirm flow flips it for one-off cases.
	AllowTaintedRead bool

	// IdempotencyKeyRequired forces the bearer to send an explicit
	// idempotency key on mutating ops. Mostly for `share` / batch
	// destructive flows.
	IdempotencyKeyRequired bool

	// SourceIPCIDR optionally pins the capability to clients whose
	// observed IP is in the listed CIDRs. Empty = unconstrained.
	SourceIPCIDR []string
}
