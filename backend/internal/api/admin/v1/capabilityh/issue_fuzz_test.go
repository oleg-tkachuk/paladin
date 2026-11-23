package capabilityh

import (
	"testing"
	"time"
	"uuid"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// issueCodes is what Issue answers an issuance the caller got wrong with:
// malformed terms, or terms it may not grant.
var issueCodes = []connect.Code{connect.CodeInvalidArgument, connect.CodePermissionDenied, connect.CodeFailedPrecondition}

// issueFuzz is one issuance as a fuzzer varies it: the subject, one op,
// resource prefix, resource URI and source CIDR, the budget, the lifetime and
// the key binding.
type issueFuzz struct {
	kind                                int32
	tenant, subject, audience, op       string
	prefix, uri, cidr, currency, jkt    string
	units, ttlSeconds, notBeforeSeconds int64
	maxRequests                         int64
	requireBiscuit                      bool
}

func (c issueFuzz) request() *adminv1.CapabilityServiceIssueRequest {
	caveats := &adminv1.CapabilityCaveats{
		Ops:            []string{c.op},
		RequestCeiling: &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
		BudgetCeiling:  &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: &money.Money{CurrencyCode: c.currency, Units: c.units}},
	}
	if c.maxRequests != 0 {
		caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: c.maxRequests}
	}
	if c.prefix != "" {
		caveats.ResourcePrefixes = []string{c.prefix}
	}
	if c.uri != "" {
		caveats.ResourceUris = []string{c.uri}
	}
	if c.cidr != "" {
		caveats.SourceIpCidr = []string{c.cidr}
	}
	req := &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind(c.kind), TenantId: c.tenant, Subject: c.subject,
		},
		Audience:        []string{c.audience},
		TtlSeconds:      c.ttlSeconds,
		Caveats:         caveats,
		ConfirmationJkt: c.jkt,
		RequireBiscuit:  c.requireBiscuit,
	}
	if c.notBeforeSeconds != 0 {
		req.NotBefore = &timestamppb.Timestamp{Seconds: c.notBeforeSeconds}
	}
	return req
}

// Whatever terms an issuer asks for, Issue mints them — for the lifetime asked
// for — or refuses them as the caller's mistake, in words that name nothing of
// the server. A request the
// contract's own rules refuse is answered by the validating interceptor before
// Issue runs, so it is skipped here.
func FuzzIssue(f *testing.F) {
	tenant := uuid.New().String()
	service := int32(adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE)
	for _, c := range []issueFuzz{
		{kind: service, tenant: tenant, subject: "svc", audience: "data", op: string(limes.OpGet), currency: "EUR", units: 10, ttlSeconds: 300},
		{kind: service, tenant: tenant, subject: "svc", audience: "data", op: string(limes.OpPut), prefix: "tenants/a/", uri: "paladin://x", cidr: "10.0.0.0/8", ttlSeconds: 60, maxRequests: 5},
		{kind: service, tenant: tenant, subject: "svc", audience: "data", op: "share", currency: "not-a-currency", units: 1, jkt: testJKT, requireBiscuit: true},
		{kind: service, tenant: tenant, subject: "svc", audience: "data", op: "get", cidr: "fe80::1%eth0/64", ttlSeconds: 1 << 40, notBeforeSeconds: -1},
		{kind: service, tenant: tenant, subject: "svc", audience: "data", op: "get", ttlSeconds: apiutil.MaxTTLSeconds + 1},
		{kind: service, tenant: tenant, subject: "svc", audience: "data", op: "get", ttlSeconds: 1 << 62},
		{kind: 2, tenant: tenant, subject: "agent\x00", audience: "", op: "frobnicate", prefix: "paladin://tenants/", ttlSeconds: -5, requireBiscuit: true},
	} {
		f.Add(c.kind, c.tenant, c.subject, c.audience, c.op, c.prefix, c.uri, c.cidr, c.currency, c.jkt,
			c.units, c.ttlSeconds, c.notBeforeSeconds, c.maxRequests, c.requireBiscuit)
	}
	validator, err := protovalidate.New()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, kind int32, tenant, subject, audience, op, prefix, uri, cidr, currency, jkt string,
		units, ttlSeconds, notBeforeSeconds, maxRequests int64, requireBiscuit bool,
	) {
		c := issueFuzz{
			kind: kind, tenant: tenant, subject: subject, audience: audience, op: op,
			prefix: prefix, uri: uri, cidr: cidr, currency: currency, jkt: jkt,
			units: units, ttlSeconds: ttlSeconds, notBeforeSeconds: notBeforeSeconds,
			maxRequests: maxRequests, requireBiscuit: requireBiscuit,
		}
		req := c.request()
		if validator.Validate(req) != nil {
			return
		}
		h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
		resp, err := h.Issue(adminCtx(), req)
		fuzzassert.Refusal(t, err, issueCodes, tenant, subject, audience, op, prefix, uri, cidr, currency, jkt)
		if err != nil || ttlSeconds == 0 {
			return
		}
		minted := resp.GetCapability()
		if got, want := minted.GetExpiresAt().AsTime().Sub(minted.GetIssuedAt().AsTime()), time.Duration(ttlSeconds)*time.Second; got != want {
			t.Fatalf("ttl_seconds %d minted a lifetime of %v, want %v", ttlSeconds, got, want)
		}
	})
}
