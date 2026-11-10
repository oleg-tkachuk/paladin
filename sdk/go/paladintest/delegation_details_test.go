package paladintest_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// delegationDetailsTable is what the server tells a caller of a delegation
// wider than its parent, by rule, which both SDKs' fakes are held to.
const delegationDetailsTable = "../../testdata/delegation_details.json"

type delegationDetails struct {
	Title   string            `json:"title"`
	Details map[string]string `json:"details"`
}

func readDelegationDetails(t *testing.T) delegationDetails {
	t.Helper()
	raw, err := os.ReadFile(delegationDetailsTable)
	if err != nil {
		t.Fatal(err)
	}
	var d delegationDetails
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// Every detail in the table is limes's own for its rule, and the title is
// its problem's: the table is the library's words, not a copy that drifts.
func TestDelegationDetailsAreLimess(t *testing.T) {
	d := readDelegationDetails(t)
	now := time.Now().Truncate(time.Second)
	tenant := uuid.New()
	const (
		parentPrefix = "object://t/a/"
		otherPrefix  = "object://t/b/"
	)
	parent := func() limes.Capability {
		return limes.Capability{
			Subject:  limes.Principal{TenantID: tenant},
			Audience: []string{paladin.AudienceData},
			Caveats:  limes.Caveats{Ops: []limes.Op{limes.OpGet}},
			IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	restricted := func() limes.Capability {
		p := parent()
		p.Caveats.ResourcePrefixes = []string{parentPrefix}
		return p
	}
	cases := map[string]struct{ parent, child func() limes.Capability }{
		"audience": {parent, func() limes.Capability {
			c := parent()
			c.Audience = append(c.Audience, paladin.PlaneNames(paladin.PlaneAdmin)...)
			return c
		}},
		"starts_before":          {parent, func() limes.Capability { c := parent(); c.IssuedAt = now.Add(-time.Minute); return c }},
		"tenant":                 {parent, func() limes.Capability { c := parent(); c.Subject.TenantID = uuid.New(); return c }},
		"key_bound":              {func() limes.Capability { p := parent(); p.ConfirmationJKT = "jkt"; return p }, parent},
		"op":                     {parent, func() limes.Capability { c := parent(); c.Caveats.Ops = []limes.Op{limes.OpPut}; return c }},
		"resources_unrestricted": {restricted, parent},
		"resource_prefix": {restricted, func() limes.Capability {
			c := restricted()
			c.Caveats.ResourcePrefixes = []string{otherPrefix}
			return c
		}},
		"resource_uri": {restricted, func() limes.Capability {
			c := parent()
			c.Caveats.ResourceURIs = []string{otherPrefix + "k"}
			return c
		}},
		"tainted_read": {parent, func() limes.Capability { c := parent(); c.Caveats.AllowTaintedRead = true; return c }},
		"idempotency_key": {func() limes.Capability {
			p := parent()
			p.Caveats.IdempotencyKeyRequired = true
			return p
		}, parent},
	}
	if len(cases) != len(d.Details) {
		t.Errorf("%d rules tested, %d in the table", len(cases), len(d.Details))
	}
	for rule, tc := range cases {
		err := limes.Narrows(tc.parent(), tc.child())
		p, ok := limes.ProblemOf(err)
		if !ok || !errors.Is(err, limes.ErrDelegationTooWide) {
			t.Errorf("%s: %v, want the delegation too wide", rule, err)
			continue
		}
		if p.Title != d.Title || p.Detail != d.Details[rule] {
			t.Errorf("%s: limes tells %q / %q, the table %q / %q", rule, p.Title, p.Detail, d.Title, d.Details[rule])
		}
	}
}

// The fake answers a delegation wider than its parent as the server does:
// the title, then which part of the request was refused.
func TestDelegateTellsWhatWasTooWide(t *testing.T) {
	d := readDelegationDetails(t)
	srv := paladintest.New(t)
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	parent := issue(t, admin, issueRequest(srv, paladin.OpGet, paladin.OpShare))
	as := srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability
	_, err := as.Delegate(context.Background(), &adminv1.CapabilityServiceDelegateRequest{
		ParentId: parent.GetCapability().GetId(),
		Caveats:  &adminv1.CapabilityCaveats{Ops: paladin.OpNames(paladin.OpPut)},
	})
	ce, ok := errors.AsType[*connect.Error](err)
	want := d.Title + ": " + d.Details["op"]
	if !ok || ce.Code() != connect.CodePermissionDenied || ce.Message() != want || paladin.LimesCode(err) != string(limes.CodeDelegationTooWide) {
		t.Errorf("err = %v, want PermissionDenied telling %q", err, want)
	}
}
