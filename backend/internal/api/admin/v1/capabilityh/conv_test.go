package capabilityh

import (
	"math"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The proto↔domain converters sit in front of every Issue/Delegate call, so a
// silent mis-mapping here would mint capabilities with the wrong scope. They
// are pure, so they test directly.

var convTenant = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// The agent metadata the principal conversions carry through unchanged.
const (
	convAgentType    = "coding-agent"
	convAgentVersion = "1.2.3"
	convModel        = "model-a"
	convMCPClient    = "cli"
)

// ─── protoToPrincipalKind ──────────────────────────────────────────────────

// platformUnit is the platform currency these tests issue in, as
// capability.currency names it.
const platformUnit = "USD"

func TestProtoToPrincipalKind(t *testing.T) {
	cases := map[adminv1.PrincipalKind]limes.PrincipalType{
		adminv1.PrincipalKind_PRINCIPAL_KIND_USER:    limes.PrincipalTypeUser,
		adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT:   limes.PrincipalTypeAgent,
		adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE: limes.PrincipalTypeService,
		// Unknown must map to the empty type so downstream validation rejects
		// it, rather than silently defaulting to a real principal kind.
		adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED: limes.PrincipalType(""),
		adminv1.PrincipalKind(99):                        limes.PrincipalType(""),
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
	if got.Type != limes.PrincipalTypeUser || got.TenantID != convTenant || got.Subject != "alice" {
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
		Kind:         adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		TenantId:     convTenant.String(),
		Subject:      "agent-1",
		AgentType:    convAgentType,
		AgentVersion: convAgentVersion,
		Model:        convModel,
		McpClient:    convMCPClient,
		RunId:        runID.String(),
		ParentRunId:  parentID.String(),
	})
	if err != nil {
		t.Fatalf("protoToPrincipal: %v", err)
	}
	if got.Agent == nil {
		t.Fatal("an agent principal must carry agent metadata")
	}
	if got.Agent.AgentType != convAgentType || got.Agent.AgentVersion != convAgentVersion ||
		got.Agent.Model != convModel || got.Agent.MCPClient != convMCPClient {
		t.Errorf("agent metadata = %+v", got.Agent)
	}
	// The run/parent lineage is what makes delegated agent calls attributable.
	if got.Agent.RunID != runID || got.Agent.ParentRunID != parentID {
		t.Errorf("lineage = %v / %v", got.Agent.RunID, got.Agent.ParentRunID)
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
	if got.Agent.RunID != uuid.Nil() || got.Agent.ParentRunID != uuid.Nil() {
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
	t.Run("parent_run_id", func(t *testing.T) {
		_, err := protoToPrincipal(&adminv1.CapabilityPrincipal{
			Kind:     adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
			TenantId: convTenant.String(), ParentRunId: "nope",
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
	if got.Budget.UnitCode != "" {
		t.Errorf("a nil caveats message must yield the zero value, got %+v", got)
	}
}

// An unlimited budget that names no currency is in the default unit, not a
// blank one.
func TestProtoToCaveatsDefaultsUnitCode(t *testing.T) {
	got := mustCaveats(t, openCaveats())
	if got.Budget.UnitCode != platformUnit {
		t.Errorf("UnitCode = %q, want the %q default", got.Budget.UnitCode, platformUnit)
	}
}

func TestProtoToCaveatsKeepsExplicitUnitCode(t *testing.T) {
	for name, c := range map[string]*adminv1.CapabilityCaveats{
		"a limit":   {BudgetCeiling: &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: &money.Money{CurrencyCode: "EUR"}}},
		"unlimited": {BudgetCeiling: unlimitedBudget("EUR")},
	} {
		if got := mustCaveats(t, c); got.Budget.UnitCode != "EUR" {
			t.Errorf("%s: UnitCode = %q, want EUR", name, got.Budget.UnitCode)
		}
	}
}

func TestProtoToCaveatsRefusesAnUnknownUnlimitedCurrency(t *testing.T) {
	_, err := protoToCaveats(&adminv1.CapabilityCaveats{BudgetCeiling: unlimitedBudget("XYZ")}, nil, platformUnit)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
}

func TestProtoToCaveatsProjectsEveryField(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{
		ResourcePrefixes:       []string{"tenants/a/"},
		ResourceUris:           []string{"paladin://x"},
		RequestCeiling:         &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 10},
		BudgetCeiling:          &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: &money.Money{CurrencyCode: "UAH", Units: 250}},
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
	if got.Requests != (limes.RequestCeiling{Max: 10}) || got.Budget != (limes.BudgetCeiling{Max: 250 * limes.NanosPerUnit, UnitCode: "UAH"}) {
		t.Errorf("limits = %s / %s %s", got.Requests, got.Budget, got.Budget.UnitCode)
	}
	// These three are the security-relevant caveats; a dropped flag widens the
	// capability beyond what the issuer asked for.
	if !got.AllowTaintedRead || !got.IdempotencyKeyRequired {
		t.Errorf("security caveats = %+v", got)
	}
	if len(got.SourceIPCIDR) != 1 || got.SourceIPCIDR[0] != "10.0.0.0/8" {
		t.Errorf("SourceIPCIDR = %v", got.SourceIPCIDR)
	}
	if len(got.Ops) != 2 || got.Ops[0] != limes.Op("read") || got.Ops[1] != limes.Op("write") {
		t.Errorf("Ops = %v", got.Ops)
	}
}

func mustCaveats(t *testing.T, c *adminv1.CapabilityCaveats) limes.Caveats {
	t.Helper()
	got, err := protoToCaveats(c, nil, platformUnit)
	if err != nil {
		t.Fatalf("protoToCaveats: %v", err)
	}
	return got
}

// The budget arrives as Money, exact to the nano.
func TestProtoToCaveatsBudgetMoney(t *testing.T) {
	got := mustCaveats(t, &adminv1.CapabilityCaveats{BudgetCeiling: &adminv1.CapabilityCaveats_MaxBudget{
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: 19, Nanos: 990_000_000},
	}})
	if want := (limes.BudgetCeiling{Max: limes.MustParseAmount("19.99"), UnitCode: "USD"}); got.Budget != want {
		t.Errorf("budget = %s, want %s", got.Budget, want)
	}
	_, err := protoToCaveats(&adminv1.CapabilityCaveats{BudgetCeiling: &adminv1.CapabilityCaveats_MaxBudget{
		MaxBudget: &money.Money{CurrencyCode: "USD", Units: -1},
	}}, nil, platformUnit)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("negative: err = %v, want InvalidArgument", err)
	}
}

// A client built before max_budget_amount was removed still sends it. Read as
// absent, its capability would be issued with no budget at all.
func TestProtoToCaveatsRefusesTheRemovedDouble(t *testing.T) {
	old := &adminv1.CapabilityCaveats{Ops: []string{"get"}}
	removed := old.ProtoReflect().Descriptor().ReservedRanges().Get(0)[0]
	raw, err := proto.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, removed, protowire.Fixed64Type)
	raw = protowire.AppendFixed64(raw, math.Float64bits(25))
	if err := proto.Unmarshal(raw, old); err != nil {
		t.Fatal(err)
	}
	if _, err := protoToCaveats(old, nil, platformUnit); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
}

// A ceiling of 0 allows nothing, as limes reads it; "no limit" is its own
// arm. A 0 read as unlimited would make a frozen capability impossible to
// issue.
func TestProtoToCaveatsCeilings(t *testing.T) {
	for name, tc := range map[string]struct {
		c        *adminv1.CapabilityCaveats
		requests limes.RequestCeiling
		budget   limes.BudgetCeiling
	}{
		"zero allows nothing": {
			c: &adminv1.CapabilityCaveats{
				RequestCeiling: &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 0},
				BudgetCeiling:  &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: &money.Money{CurrencyCode: "USD"}},
			},
			budget: limes.BudgetCeiling{UnitCode: "USD"},
		},
		"a limit": {
			c: &adminv1.CapabilityCaveats{
				RequestCeiling: &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 7},
				BudgetCeiling:  &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: &money.Money{CurrencyCode: "USD", Units: 2}},
			},
			requests: limes.RequestCeiling{Max: 7},
			budget:   limes.BudgetCeiling{Max: 2 * limes.NanosPerUnit, UnitCode: "USD"},
		},
		"unlimited": {
			c:        openCaveats(),
			requests: limes.RequestCeiling{Unlimited: true},
			budget:   limes.BudgetCeiling{Unlimited: true, UnitCode: platformUnit},
		},
	} {
		got := mustCaveats(t, tc.c)
		if got.Requests != tc.requests || got.Budget != tc.budget {
			t.Errorf("%s: ceilings = %s / %s, want %s / %s", name, got.Requests, got.Budget, tc.requests, tc.budget)
		}
	}
}

// An unset ceiling keeps the parent's, and the parent's unit with its budget;
// a set one replaces it.
func TestProtoToCaveatsInheritsUnsetCeilings(t *testing.T) {
	parent := limes.Caveats{
		Requests: limes.RequestCeiling{Max: 9},
		Budget:   limes.BudgetCeiling{Max: limes.NanosPerUnit, UnitCode: "EUR"},
	}
	got, err := protoToCaveats(&adminv1.CapabilityCaveats{}, &parent, platformUnit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != parent.Requests || got.Budget != parent.Budget || got.Budget.UnitCode != parent.Budget.UnitCode {
		t.Errorf("unset: %s / %s %s, want the parent's %s / %s %s",
			got.Requests, got.Budget, got.Budget.UnitCode, parent.Requests, parent.Budget, parent.Budget.UnitCode)
	}
	got, err = protoToCaveats(&adminv1.CapabilityCaveats{
		RequestCeiling: &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 0},
	}, &parent, platformUnit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != (limes.RequestCeiling{}) || got.Budget != parent.Budget {
		t.Errorf("requests set to 0: %s / %s, want 0 / the parent's %s", got.Requests, got.Budget, parent.Budget)
	}
}

func TestRequireCeilings(t *testing.T) {
	for name, c := range map[string]*adminv1.CapabilityCaveats{
		"neither":     {},
		"no requests": {BudgetCeiling: unlimitedBudget("")},
		"no budget":   {RequestCeiling: &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true}},
	} {
		if err := requireCeilings(c); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
	if err := requireCeilings(openCaveats()); err != nil {
		t.Errorf("both set: %v", err)
	}
}

// Each ceiling reads back on its own arm, so a client can tell "no limit"
// from "nothing allowed".
func TestCaveatsToProtoCeilings(t *testing.T) {
	open := caveatsToProto(limes.Caveats{
		Requests: limes.RequestCeiling{Unlimited: true}, Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: "EUR"},
	})
	if !open.GetUnlimitedRequests() || open.GetUnlimitedBudget().GetCurrencyCode() != "EUR" {
		t.Errorf("unlimited: %v / %v", open.GetRequestCeiling(), open.GetBudgetCeiling())
	}
	frozen := caveatsToProto(limes.Caveats{Budget: limes.BudgetCeiling{UnitCode: "EUR"}})
	if _, ok := frozen.GetRequestCeiling().(*adminv1.CapabilityCaveats_MaxRequests); !ok || frozen.GetMaxRequests() != 0 {
		t.Errorf("zero requests: %v", frozen.GetRequestCeiling())
	}
	if want := (&money.Money{CurrencyCode: "EUR"}); !proto.Equal(frozen.GetMaxBudget(), want) {
		t.Errorf("zero budget: %v, want %v", frozen.GetBudgetCeiling(), want)
	}
}

func TestCaveatsToProtoBudgetMoney(t *testing.T) {
	out := caveatsToProto(limes.Caveats{Budget: limes.BudgetCeiling{Max: limes.MustParseAmount("0.3"), UnitCode: "EUR"}})
	if want := (&money.Money{CurrencyCode: "EUR", Nanos: 300_000_000}); !proto.Equal(out.GetMaxBudget(), want) {
		t.Errorf("budget = %v, want %v", out.GetMaxBudget(), want)
	}
}

func TestProtoToCaveatsNoOps(t *testing.T) {
	got := mustCaveats(t, openCaveats())
	if len(got.Ops) != 0 {
		t.Errorf("Ops = %v, want none", got.Ops)
	}
}

// ─── reverse converters ────────────────────────────────────────────────────

func TestPrincipalKindToProto(t *testing.T) {
	cases := map[limes.PrincipalType]adminv1.PrincipalKind{
		limes.PrincipalTypeUser:      adminv1.PrincipalKind_PRINCIPAL_KIND_USER,
		limes.PrincipalTypeAgent:     adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		limes.PrincipalTypeService:   adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE,
		limes.PrincipalType(""):      adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED,
		limes.PrincipalType("weird"): adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED,
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
	for _, k := range []limes.PrincipalType{
		limes.PrincipalTypeUser, limes.PrincipalTypeAgent, limes.PrincipalTypeService,
	} {
		if got := protoToPrincipalKind(principalKindToProto(k)); got != k {
			t.Errorf("round trip of %q gave %q", k, got)
		}
	}
}

func TestPrincipalToProtoUser(t *testing.T) {
	got := principalToProto(limes.Principal{
		Type: limes.PrincipalTypeUser, TenantID: convTenant, Subject: "alice",
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
	got := principalToProto(limes.Principal{
		Type: limes.PrincipalTypeAgent, TenantID: convTenant, Subject: "agent-1",
		Agent: &limes.AgentPrincipal{
			AgentType: convAgentType, AgentVersion: convAgentVersion, Model: convModel, MCPClient: convMCPClient,
			RunID: runID, ParentRunID: parentID,
		},
	})
	if got.GetAgentType() != convAgentType || got.GetAgentVersion() != convAgentVersion ||
		got.GetModel() != convModel || got.GetMcpClient() != convMCPClient {
		t.Errorf("agent metadata = %+v", got)
	}
	if got.GetRunId() != runID.String() || got.GetParentRunId() != parentID.String() {
		t.Errorf("lineage = %q / %q", got.GetRunId(), got.GetParentRunId())
	}
}

// A zero lineage id must be omitted rather than serialised as the all-zero
// UUID, which a client would read as a real parent.
func TestPrincipalToProtoOmitsZeroLineage(t *testing.T) {
	got := principalToProto(limes.Principal{
		Type:  limes.PrincipalTypeAgent,
		Agent: &limes.AgentPrincipal{AgentType: "x"},
	})
	if got.GetRunId() != "" || got.GetParentRunId() != "" {
		t.Errorf("zero lineage must be omitted, got %q / %q", got.GetRunId(), got.GetParentRunId())
	}
}

// A full round trip through both directions must preserve the agent identity.
func TestPrincipalRoundTrip(t *testing.T) {
	runID := uuid.New()
	in := limes.Principal{
		Type: limes.PrincipalTypeAgent, TenantID: convTenant, Subject: "agent-1",
		Agent: &limes.AgentPrincipal{AgentType: convAgentType, RunID: runID},
	}

	got, err := protoToPrincipal(principalToProto(in))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got.Type != in.Type || got.TenantID != in.TenantID || got.Subject != in.Subject {
		t.Errorf("principal = %+v, want %+v", got, in)
	}
	if got.Agent == nil || got.Agent.AgentType != convAgentType || got.Agent.RunID != runID {
		t.Errorf("agent = %+v", got.Agent)
	}
}

// openCaveats are caveats allowing ops, with no request or budget limit.
func openCaveats(ops ...limes.Op) *adminv1.CapabilityCaveats {
	c := &adminv1.CapabilityCaveats{
		RequestCeiling: &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
		BudgetCeiling:  unlimitedBudget(""),
	}
	for _, op := range ops {
		c.Ops = append(c.Ops, string(op))
	}
	return c
}

// unlimitedBudget is a budget with no limit, in currency.
func unlimitedBudget(currency string) *adminv1.CapabilityCaveats_UnlimitedBudget {
	return &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{CurrencyCode: currency}}
}

// A budget that names no currency is the platform's at the root and the
// parent's under one: a delegate counts in what it draws from.
func TestProtoToCaveatsResolvesAnUnnamedUnit(t *testing.T) {
	unnamed := &adminv1.CapabilityCaveats{BudgetCeiling: unlimitedBudget("")}

	root, err := protoToCaveats(unnamed, nil, limes.AbstractUnitCode)
	if err != nil {
		t.Fatal(err)
	}
	if root.Budget.UnitCode != limes.AbstractUnitCode {
		t.Errorf("root unit = %q, want the platform's %q", root.Budget.UnitCode, limes.AbstractUnitCode)
	}

	parent := limes.Caveats{Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: "EUR"}}
	child, err := protoToCaveats(unnamed, &parent, limes.AbstractUnitCode)
	if err != nil {
		t.Fatal(err)
	}
	if child.Budget.UnitCode != "EUR" {
		t.Errorf("delegate unit = %q, want the parent's EUR", child.Budget.UnitCode)
	}

	named, err := protoToCaveats(&adminv1.CapabilityCaveats{BudgetCeiling: unlimitedBudget("JPY")}, nil, limes.AbstractUnitCode)
	if err != nil {
		t.Fatal(err)
	}
	if named.Budget.UnitCode != "JPY" {
		t.Errorf("named unit = %q, want JPY", named.Budget.UnitCode)
	}
}
