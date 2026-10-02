package capabilityh

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The proto↔domain converters sit in front of every Issue/Delegate call, so a
// silent mis-mapping here would mint capabilities with the wrong scope. They
// are pure, so they test directly.

var convTenant = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// ─── protoToPrincipalKind ──────────────────────────────────────────────────

func TestProtoToPrincipalKind(t *testing.T) {
	cases := map[adminv1.PrincipalKind]capability.PrincipalType{
		adminv1.PrincipalKind_PRINCIPAL_KIND_USER:    capability.PrincipalUser,
		adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT:   capability.PrincipalAgent,
		adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE: capability.PrincipalService,
		// Unknown must map to the empty type so downstream validation rejects
		// it, rather than silently defaulting to a real principal kind.
		adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED: capability.PrincipalType(""),
		adminv1.PrincipalKind(99):                        capability.PrincipalType(""),
	}
	for in, want := range cases {
		if got := protoToPrincipalKind(in); got != want {
			t.Errorf("protoToPrincipalKind(%v) = %q, want %q", in, got, want)
		}
	}
}

// ─── protoToPrincipal ──────────────────────────────────────────────────────

func TestProtoToPrincipalNil(t *testing.T) {
	if _, err := protoToPrincipal(nil); err == nil {
		t.Fatal("a nil principal must be rejected, not silently zero-valued")
	}
}

func TestProtoToPrincipalUser(t *testing.T) {
	got, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
		Kind:     adminv1.PrincipalKind_PRINCIPAL_KIND_USER,
		TenantId: convTenant.String(),
		Subject:  "alice",
	})
	if err != nil {
		t.Fatalf("protoToPrincipal: %v", err)
	}
	if got.Type != capability.PrincipalUser || got.TenantID != convTenant || got.Subject != "alice" {
		t.Errorf("principal = %+v", got)
	}
	// Agent detail must stay nil for a non-agent principal.
	if got.Agent != nil {
		t.Error("a user principal must not carry agent metadata")
	}
}

// A malformed tenant id must fail rather than falling through as uuid.Nil,
// which would scope the capability to no tenant at all.
func TestProtoToPrincipalRejectsBadTenant(t *testing.T) {
	for _, id := range []string{"", "not-a-uuid"} {
		_, err := protoToPrincipal(&adminv1.CapabilityPrincipal{TenantId: id})
		if err == nil {
			t.Errorf("tenant_id %q: want an error", id)
		}
	}
}

func TestProtoToPrincipalAgent(t *testing.T) {
	runID := uuid.New()
	parentID := uuid.New()

	got, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
		Kind:          adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		TenantId:      convTenant.String(),
		Subject:       "agent-1",
		AgentType:     "claude-code",
		AgentVersion:  "1.2.3",
		Model:         "opus",
		McpClient:     "cli",
		RunId:         runID.String(),
		ParentAgentId: parentID.String(),
	})
	if err != nil {
		t.Fatalf("protoToPrincipal: %v", err)
	}
	if got.Agent == nil {
		t.Fatal("an agent principal must carry agent metadata")
	}
	if got.Agent.AgentType != "claude-code" || got.Agent.AgentVersion != "1.2.3" ||
		got.Agent.Model != "opus" || got.Agent.MCPClient != "cli" {
		t.Errorf("agent metadata = %+v", got.Agent)
	}
	// The run/parent lineage is what makes delegated agent calls attributable.
	if got.Agent.RunID != runID || got.Agent.ParentAgentID != parentID {
		t.Errorf("lineage = %v / %v", got.Agent.RunID, got.Agent.ParentAgentID)
	}
}

// Lineage ids are optional; omitting them must leave them zero, not error.
func TestProtoToPrincipalAgentWithoutLineage(t *testing.T) {
	got, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
		Kind:     adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		TenantId: convTenant.String(),
	})
	if err != nil {
		t.Fatalf("protoToPrincipal: %v", err)
	}
	if got.Agent.RunID != uuid.Nil || got.Agent.ParentAgentID != uuid.Nil {
		t.Errorf("unset lineage must stay zero, got %+v", got.Agent)
	}
}

// A present-but-malformed lineage id is a client bug and must be rejected —
// silently zeroing it would detach the capability from its run.
func TestProtoToPrincipalRejectsBadLineage(t *testing.T) {
	t.Run("run_id", func(t *testing.T) {
		_, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
			Kind:     adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
			TenantId: convTenant.String(), RunId: "nope",
		})
		if err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("parent_agent_id", func(t *testing.T) {
		_, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
			Kind:     adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
			TenantId: convTenant.String(), ParentAgentId: "nope",
		})
		if err == nil {
			t.Fatal("want an error")
		}
	})
}

// Agent fields on a non-agent principal must be ignored rather than smuggled
// into the domain object.
func TestProtoToPrincipalIgnoresAgentFieldsForUser(t *testing.T) {
	got, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
		Kind:      adminv1.PrincipalKind_PRINCIPAL_KIND_USER,
		TenantId:  convTenant.String(),
		AgentType: "smuggled",
		RunId:     "also-not-a-uuid", // must not even be parsed
	})
	if err != nil {
		t.Fatalf("protoToPrincipal: %v", err)
	}
	if got.Agent != nil {
		t.Errorf("agent metadata leaked onto a user principal: %+v", got.Agent)
	}
}

// ─── protoToCaveats ────────────────────────────────────────────────────────

func TestProtoToCaveatsNil(t *testing.T) {
	got := mustCaveats(t, nil)
	if got.UnitCode != "" {
		t.Errorf("a nil caveats message must yield the zero value, got %+v", got)
	}
}

// Pre-currency-rename clients never set unit_code; the server must fill the
// default rather than minting a capability with a blank currency.
func TestProtoToCaveatsDefaultsUnitCode(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{})
	if got.UnitCode != capability.DefaultUnitCode {
		t.Errorf("UnitCode = %q, want the %q default", got.UnitCode, capability.DefaultUnitCode)
	}
}

func TestProtoToCaveatsKeepsExplicitUnitCode(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{UnitCode: "EUR"})
	if got.UnitCode != "EUR" {
		t.Errorf("UnitCode = %q, want EUR", got.UnitCode)
	}
}

func TestProtoToCaveatsProjectsEveryField(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{
		ResourcePrefixes:       []string{"tenants/a/"},
		ResourceUris:           []string{"paladin://x"},
		MaxRequests:            10,
		MaxBudgetMicros:        proto.Int64(250_000_000),
		UnitCode:               "UAH",
		AllowTaintedRead:       true,
		IdempotencyKeyRequired: true,
		SourceIpCidr:           []string{"10.0.0.0/8"},
		Ops:                    []string{"read", "write"},
	})

	if len(got.ResourcePrefixes) != 1 || got.ResourcePrefixes[0] != "tenants/a/" {
		t.Errorf("ResourcePrefixes = %v", got.ResourcePrefixes)
	}
	if len(got.ResourceURIs) != 1 || got.ResourceURIs[0] != "paladin://x" {
		t.Errorf("ResourceURIs = %v", got.ResourceURIs)
	}
	if got.MaxRequests != 10 || got.MaxBudgetAmount != 250 {
		t.Errorf("limits = %d / %v", got.MaxRequests, got.MaxBudgetAmount)
	}
	// These three are the security-relevant caveats; a dropped flag widens the
	// capability beyond what the issuer asked for.
	if !got.AllowTaintedRead || !got.IdempotencyKeyRequired {
		t.Errorf("security caveats = %+v", got)
	}
	if len(got.SourceIPCIDR) != 1 || got.SourceIPCIDR[0] != "10.0.0.0/8" {
		t.Errorf("SourceIPCIDR = %v", got.SourceIPCIDR)
	}
	if len(got.Ops) != 2 || got.Ops[0] != capability.Op("read") || got.Ops[1] != capability.Op("write") {
		t.Errorf("Ops = %v", got.Ops)
	}
}

func mustCaveats(t *testing.T, c *adminv1.CapabilityCaveats) capability.Caveats {
	t.Helper()
	got, err := protoToCaveats(c)
	if err != nil {
		t.Fatalf("protoToCaveats: %v", err)
	}
	return got
}

// The budget arrives in micros from a current client, as a double from an
// old one, and both from a client in transition: they must then agree.
func TestProtoToCaveatsBudgetMicros(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{MaxBudgetMicros: proto.Int64(19_990_000)})
	if got.MaxBudgetAmount != 19.99 {
		t.Errorf("micros only: budget = %v, want 19.99", got.MaxBudgetAmount)
	}
	got = mustCaveats(t, &adminv1.CapabilityCaveats{MaxBudgetAmount: 19.99, MaxBudgetMicros: proto.Int64(19_990_000)}) //nolint:staticcheck // the transition case
	if got.MaxBudgetAmount != 19.99 {
		t.Errorf("both, agreeing: budget = %v", got.MaxBudgetAmount)
	}
	_, err := protoToCaveats(&adminv1.CapabilityCaveats{MaxBudgetAmount: 20, MaxBudgetMicros: proto.Int64(19_990_000)}) //nolint:staticcheck // the transition case
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("both, disagreeing: err = %v, want InvalidArgument", err)
	}
}

func TestCaveatsToProtoFillsBothBudgetFields(t *testing.T) {
	out := caveatsToProto(capability.Caveats{MaxBudgetAmount: 0.3})
	//nolint:staticcheck // the deprecated double is still filled for old clients
	if out.GetMaxBudgetMicros() != 300_000 || out.GetMaxBudgetAmount() != 0.3 {
		t.Errorf("budget = %d micros / %v", out.GetMaxBudgetMicros(), out.GetMaxBudgetAmount()) //nolint:staticcheck // as above
	}
}

func TestProtoToCaveatsNoOps(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{})
	if len(got.Ops) != 0 {
		t.Errorf("Ops = %v, want none", got.Ops)
	}
}

// ─── reverse converters ────────────────────────────────────────────────────

func TestPrincipalKindToProto(t *testing.T) {
	cases := map[capability.PrincipalType]adminv1.PrincipalKind{
		capability.PrincipalUser:          adminv1.PrincipalKind_PRINCIPAL_KIND_USER,
		capability.PrincipalAgent:         adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		capability.PrincipalService:       adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE,
		capability.PrincipalType(""):      adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED,
		capability.PrincipalType("weird"): adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := principalKindToProto(in); got != want {
			t.Errorf("principalKindToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

// The kind mapping is used in both directions on the issue/list path, so a
// round trip must be lossless for every real kind.
func TestPrincipalKindRoundTrip(t *testing.T) {
	for _, k := range []capability.PrincipalType{
		capability.PrincipalUser, capability.PrincipalAgent, capability.PrincipalService,
	} {
		if got := protoToPrincipalKind(principalKindToProto(k)); got != k {
			t.Errorf("round trip of %q gave %q", k, got)
		}
	}
}

func TestPrincipalToProtoUser(t *testing.T) {
	got := principalToProto(capability.Principal{
		Type: capability.PrincipalUser, TenantID: convTenant, Subject: "alice",
	})
	if got.GetKind() != adminv1.PrincipalKind_PRINCIPAL_KIND_USER {
		t.Errorf("Kind = %v", got.GetKind())
	}
	if got.GetTenantId() != convTenant.String() || got.GetSubject() != "alice" {
		t.Errorf("principal = %+v", got)
	}
	// Agent-only fields must stay empty for a user.
	if got.GetAgentType() != "" || got.GetRunId() != "" {
		t.Errorf("agent fields leaked: %+v", got)
	}
}

func TestPrincipalToProtoAgent(t *testing.T) {
	runID, parentID := uuid.New(), uuid.New()
	got := principalToProto(capability.Principal{
		Type: capability.PrincipalAgent, TenantID: convTenant, Subject: "agent-1",
		Agent: &capability.AgentPrincipal{
			AgentType: "claude-code", AgentVersion: "1.2.3", Model: "opus", MCPClient: "cli",
			RunID: runID, ParentAgentID: parentID,
		},
	})
	if got.GetAgentType() != "claude-code" || got.GetAgentVersion() != "1.2.3" ||
		got.GetModel() != "opus" || got.GetMcpClient() != "cli" {
		t.Errorf("agent metadata = %+v", got)
	}
	if got.GetRunId() != runID.String() || got.GetParentAgentId() != parentID.String() {
		t.Errorf("lineage = %q / %q", got.GetRunId(), got.GetParentAgentId())
	}
}

// A zero lineage id must be omitted rather than serialised as the all-zero
// UUID, which a client would read as a real parent.
func TestPrincipalToProtoOmitsZeroLineage(t *testing.T) {
	got := principalToProto(capability.Principal{
		Type:  capability.PrincipalAgent,
		Agent: &capability.AgentPrincipal{AgentType: "x"},
	})
	if got.GetRunId() != "" || got.GetParentAgentId() != "" {
		t.Errorf("zero lineage must be omitted, got %q / %q", got.GetRunId(), got.GetParentAgentId())
	}
}

// A full round trip through both directions must preserve the agent identity.
func TestPrincipalRoundTrip(t *testing.T) {
	runID := uuid.New()
	in := capability.Principal{
		Type: capability.PrincipalAgent, TenantID: convTenant, Subject: "agent-1",
		Agent: &capability.AgentPrincipal{AgentType: "claude-code", RunID: runID},
	}

	got, err := protoToPrincipal(principalToProto(in))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got.Type != in.Type || got.TenantID != in.TenantID || got.Subject != in.Subject {
		t.Errorf("principal = %+v, want %+v", got, in)
	}
	if got.Agent == nil || got.Agent.AgentType != "claude-code" || got.Agent.RunID != runID {
		t.Errorf("agent = %+v", got.Agent)
	}
}
