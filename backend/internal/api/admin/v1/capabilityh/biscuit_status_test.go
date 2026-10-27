package capabilityh

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A JWK thumbprint the tests bind a capability to: its shape is all the
// server checks before minting.
const testJKT = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"

// Each reason a capability goes without its Biscuit, the first that applies.
// Its ops play no part: one without share is sealed too.
func TestBiscuitStatusOf(t *testing.T) {
	now := time.Now()
	cases := map[string]struct {
		jkt  string
		nbf  time.Time
		want adminv1.BiscuitStatus
	}{
		"unbound, valid now":    {"", time.Time{}, adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED},
		"a not-before passed":   {"", now.Add(-time.Minute), adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED},
		"bound to a key":        {testJKT, time.Time{}, adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
		"bound, and later":      {testJKT, now.Add(time.Hour), adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
		"valid only from later": {"", now.Add(time.Hour), adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID},
	}
	for name, tc := range cases {
		if got := biscuitStatusOf(tc.jkt, tc.nbf, now); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func issueReq(ops ...string) *adminv1.CapabilityServiceIssueRequest {
	return &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(toOps(ops)...),
	}
}

func toOps(ops []string) []limes.Op {
	out := make([]limes.Op, 0, len(ops))
	for _, o := range ops {
		out = append(out, limes.Op(o))
	}
	return out
}

// Every answer names whether it carries the Biscuit, and why not.
func TestIssueNamesTheBiscuitStatus(t *testing.T) {
	cases := map[string]struct {
		req        *adminv1.CapabilityServiceIssueRequest
		want       adminv1.BiscuitStatus
		hasBiscuit bool
	}{
		"shareable": {issueReq(string(limes.OpGet), string(limes.OpShare)), adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED, true},
		"no share":  {issueReq(string(limes.OpGet)), adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED, true},
		"key-bound": {func() *adminv1.CapabilityServiceIssueRequest {
			r := issueReq(string(limes.OpGet))
			r.ConfirmationJkt = testJKT
			return r
		}(), adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
			resp, err := h.Issue(adminCtx(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetBiscuitStatus() != tc.want || (resp.GetBiscuit() != "") != tc.hasBiscuit || resp.GetToken() == "" {
				t.Errorf("status %v, biscuit %t, token %t", resp.GetBiscuitStatus(), resp.GetBiscuit() != "", resp.GetToken() != "")
			}
		})
	}
}

// require_biscuit refuses, before anything is issued, a capability that would
// come back without its Biscuit, naming why; one that would not is issued.
func TestIssueRequireBiscuit(t *testing.T) {
	future := timestamppb.New(time.Now().Add(time.Hour))
	cases := map[string]struct {
		mutate  func(*adminv1.CapabilityServiceIssueRequest)
		ops     []string
		refused bool
		why     string
	}{
		"shareable":     {func(*adminv1.CapabilityServiceIssueRequest) {}, []string{"get", "share"}, false, ""},
		"no share":      {func(*adminv1.CapabilityServiceIssueRequest) {}, []string{"get"}, false, ""},
		"key-bound":     {func(r *adminv1.CapabilityServiceIssueRequest) { r.ConfirmationJkt = testJKT }, []string{"get", "share"}, true, "bound to a key"},
		"not yet valid": {func(r *adminv1.CapabilityServiceIssueRequest) { r.NotBefore = future }, []string{"get", "share"}, true, "not_before"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{}
			h := mkHandler(t, store, nil, &allowAuthorizer{})
			req := issueReq(tc.ops...)
			req.RequireBiscuit = true
			tc.mutate(req)
			resp, err := h.Issue(adminCtx(), req)
			if !tc.refused {
				if err != nil || resp.GetBiscuit() == "" {
					t.Fatalf("a capability that can be sealed: %v, biscuit %t", err, resp.GetBiscuit() != "")
				}
				return
			}
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), tc.why) {
				t.Errorf("err = %v, want FailedPrecondition naming %q", err, tc.why)
			}
			if store.inserted != 0 {
				t.Errorf("%d capabilities issued for a refused request", store.inserted)
			}
		})
	}
}

// Delegate holds require_biscuit to the child as limes mints it: the key
// binding it inherits or names. A child narrowed off share is sealed too.
func TestDelegateRequireBiscuit(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := delegateHandler(m, &denyAuthorizer{})
	ctx := auth.WithCapability(context.Background(), parent)

	inherits := delegateReq(parent)
	inherits.RequireBiscuit = true
	resp, err := h.Delegate(ctx, inherits)
	if err != nil || resp.GetBiscuitStatus() != adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED || resp.GetBiscuit() == "" {
		t.Fatalf("a child that keeps share: %v, status %v", err, resp.GetBiscuitStatus())
	}

	narrowed := delegateReq(parent)
	narrowed.RequireBiscuit = true
	narrowed.Caveats = openCaveats(limes.OpGet)
	resp, err = h.Delegate(ctx, narrowed)
	if err != nil || resp.GetBiscuitStatus() != adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED || resp.GetBiscuit() == "" {
		t.Errorf("a child narrowed off share: %v, status %v", err, resp.GetBiscuitStatus())
	}

	bound := delegateReq(parent)
	bound.RequireBiscuit = true
	bound.ConfirmationJkt = testJKT
	if _, err := h.Delegate(ctx, bound); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a child bound to a key: %v, want FailedPrecondition", err)
	}
}
