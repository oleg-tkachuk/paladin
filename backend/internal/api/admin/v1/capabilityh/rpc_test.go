package capabilityh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// Drives Issue / Revoke / List / GetUsage through the handler with the
// package's existing fakes. The Cedar gate is asserted per RPC — these mint
// and revoke authority, so an ungated path is the worst failure mode here.

func adminCtx() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "operator", TenantID: uuid.New(), Roles: []string{"platform.admin"},
	})
}

func codeOf(err error) connect.Code { return connect.CodeOf(err) }

// recordingStore extends the package fake with argument capture and
// configurable failures for the paths the existing tests do not drive.
type recordingStore struct {
	fakeStore
	revokeArgs *limes.RevokeRequest
	revokeErr  error
	// revokeResult is what Revoke answers when it does not fail.
	revokeResult limes.RevokeResult
	revokeCtx    context.Context
	listArgs     *limes.ListRecordsRequest
	listOut      []limes.RecordListing
	listNext     string
	listErr      error
}

func (s *recordingStore) Revoke(ctx context.Context, args limes.RevokeRequest) (limes.RevokeResult, error) {
	s.revokeArgs = &args
	s.revokeCtx = ctx
	return s.revokeResult, s.revokeErr
}

func (s *recordingStore) GetRecordListing(context.Context, uuid.UUID) (limes.RecordListing, error) {
	return limes.RecordListing{}, errors.New("not used")
}

// ListRecords makes recordingStore the handler's record lister too.
func (s *recordingStore) ListRecords(_ context.Context, args limes.ListRecordsRequest) (limes.Page[limes.RecordListing], error) {
	s.listArgs = &args
	if s.listErr != nil {
		return limes.Page[limes.RecordListing]{}, s.listErr
	}
	return limes.Page[limes.RecordListing]{Items: s.listOut, Next: s.listNext}, nil
}

// fakeUsage embeds the interface so only Get needs implementing; any other
// method the handler might start calling panics loudly instead of silently
// returning a zero value.
type fakeUsage struct {
	meter.UsageStore
	out meter.Usage
	err error
	got uuid.UUID
	ctx context.Context
}

func (u *fakeUsage) GetUsage(ctx context.Context, id uuid.UUID) (meter.Usage, error) {
	u.got = id
	u.ctx = ctx
	return u.out, u.err
}

// ─── NewHandler ────────────────────────────────────────────────────────────

// Wiring bugs must fail at boot, not on the first request that would otherwise
// mint an ungated capability.
func TestNewHandlerPanicsOnMissingDependencies(t *testing.T) {
	store := &fakeStore{}
	m := newMinter(t, store)

	cases := map[string]func(){
		"nil issuer":   func() { NewHandler(nil, m.Verifier, store, nil, &allowAuthorizer{}) },
		"nil verifier": func() { NewHandler(m.Issuer, nil, store, nil, &allowAuthorizer{}) },
		"nil store":    func() { NewHandler(m.Issuer, m.Verifier, nil, nil, &allowAuthorizer{}) },
		"nil policy":   func() { NewHandler(m.Issuer, m.Verifier, store, nil, nil) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("want a panic")
				}
			}()
			fn()
		})
	}
}

// usage may legitimately be nil — the operator simply hasn't wired the
// counter store.
func TestNewHandlerAllowsNilUsage(t *testing.T) {
	store := &fakeStore{}
	if h := mkHandler(t, store, nil, &allowAuthorizer{}); h == nil {
		t.Fatal("NewHandler returned nil")
	}
}

// ─── Issue ─────────────────────────────────────────────────────────────────

func TestIssueMintsAToken(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	tenant := uuid.New()

	resp, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: tenant.String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet, limes.OpShare),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if resp.GetToken() == "" {
		t.Error("want a minted token")
	}
	if resp.GetCapability() == nil {
		t.Error("want the capability echoed back")
	}
	// The same capability as a Biscuit, which its holder can attenuate.
	if b := resp.GetBiscuit(); b == "" {
		t.Error("want a Biscuit beside the token")
	} else if _, err := limes.Attenuate(b, limes.Attenuation{Ops: []limes.Op{limes.OpGet}}); err != nil {
		t.Errorf("the returned Biscuit does not attenuate: %v", err)
	}
}

// A capability that may not be shared is sealed as a Biscuit too: a copy
// narrowed from it allows no more than its token, which its bearer could
// hand on as it is.
func TestIssueSealsABiscuitWithoutShare(t *testing.T) {
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
	resp, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if resp.GetToken() == "" || resp.GetBiscuitStatus() != adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		t.Fatalf("token %t, status %v: want a token and a Biscuit", resp.GetToken() != "", resp.GetBiscuitStatus())
	}
	if _, err := limes.Attenuate(resp.GetBiscuit(), limes.Attenuation{Ops: []limes.Op{limes.OpGet}}); err != nil {
		t.Errorf("the returned Biscuit does not attenuate: %v", err)
	}
}

// A key-bound capability is answered with its JWT alone: the server seals a
// Biscuit by verifying what it minted, and a key-bound token verifies only
// with a proof of its holder's key, which the server does not have.
func TestIssueKeyBoundGetsNoBiscuit(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := dpop.Thumbprint(pub)
	if err != nil {
		t.Fatal(err)
	}
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})

	resp, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "alice",
		},
		Audience:        []string{"paladin-data"},
		TtlSeconds:      300,
		Caveats:         openCaveats(limes.OpGet),
		ConfirmationJkt: jkt,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if resp.GetToken() == "" || resp.GetCapability().GetConfirmationJkt() != jkt {
		t.Errorf("token %q bound to %q; want a token bound to %q", resp.GetToken(), resp.GetCapability().GetConfirmationJkt(), jkt)
	}
	if b := resp.GetBiscuit(); b != "" {
		t.Errorf("a key-bound capability came with a Biscuit %q", b)
	}
}

// notBeforeLead puts a capability's not-before past the verifier's leeway.
const notBeforeLead = time.Hour

// A capability valid only from a later not-before is issued, without a
// Biscuit: it cannot be verified, and so sealed, until then.
func TestIssueNotYetValidGetsNoBiscuit(t *testing.T) {
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})

	resp, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: int64((2 * notBeforeLead).Seconds()),
		Caveats:    openCaveats(limes.OpGet),
		NotBefore:  timestamppb.New(time.Now().Add(notBeforeLead)),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if resp.GetToken() == "" {
		t.Error("no token")
	}
	if b := resp.GetBiscuit(); b != "" {
		t.Errorf("a capability not valid yet came with a Biscuit %q", b)
	}
}

func TestIssueIsCedarGated(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &denyAuthorizer{})

	_, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{TenantId: uuid.New().String()},
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// A malformed subject must be rejected before anything is minted.
func TestIssueRejectsBadSubject(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	for name, req := range map[string]*adminv1.CapabilityServiceIssueRequest{
		"no subject":  {},
		"bad tenant":  {Subject: &adminv1.CapabilityPrincipal{TenantId: "nope"}},
		"bad lineage": {Subject: &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: uuid.New().String(), RunId: "nope"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.Issue(adminCtx(), req)
			if codeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", codeOf(err))
			}
		})
	}
}

// ─── Revoke ────────────────────────────────────────────────────────────────

func TestRevokeForwardsArgs(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	id := uuid.New()

	_, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{
		Id: id.String(), Reason: "leaked", CascadeChildren: true,
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.revokeArgs == nil {
		t.Fatal("store.Revoke was not called")
	}
	if store.revokeArgs.CapabilityID != id || store.revokeArgs.Note != "leaked" {
		t.Errorf("args = %+v", store.revokeArgs)
	}
	// Cascade is what tears down the delegated children of a leaked token.
	if !store.revokeArgs.CascadeChildren {
		t.Error("CascadeChildren must be forwarded")
	}
	// The actor is the audit trail for who revoked it.
	if store.revokeArgs.Actor != "operator" {
		t.Errorf("Actor = %q, want operator", store.revokeArgs.Actor)
	}
}

func TestRevokeIsCedarGated(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &denyAuthorizer{})

	_, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
	if store.revokeArgs != nil {
		t.Error("a denied revoke must not reach the store")
	}
}

func TestRevokeRejectsBadID(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: "nope"})
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

// Revoke says whether the capability was revoked already, with the entry
// that stands — the first one, kept by an idempotent second revoke — and
// what the capability was just before the call.
func TestRevokeSaysWhatItDid(t *testing.T) {
	revokedAt := time.Unix(1_800_000_000, 0).UTC()
	for _, already := range []bool{false, true} {
		before := limes.RecordDispositionActive
		if already {
			before = limes.RecordDispositionRevoked
		}
		store := &recordingStore{revokeResult: limes.RevokeResult{
			AlreadyRevoked:   already,
			Revocation:       limes.Revocation{RevokedAt: revokedAt, Note: "first", Actor: "sec", Cascaded: true},
			PriorDisposition: before,
		}}
		h := mkHandler(t, store, nil, &allowAuthorizer{})
		resp, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: uuid.New().String(), Reason: "second"})
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		r := resp.GetRevocation()
		if resp.GetAlreadyRevoked() != already || r.GetReason() != "first" || r.GetActor() != "sec" || !r.GetCascade() || !r.GetRevokedAt().AsTime().Equal(revokedAt) {
			t.Errorf("already=%v: response %v, want the store's result", already, resp)
		}
		if got := resp.GetBefore(); got != dispositionToProto(before) {
			t.Errorf("already=%v: before = %v, want %v", already, got, before)
		}
	}
}

func TestRevokeStoreErrorIsInternal(t *testing.T) {
	store := &recordingStore{revokeErr: errors.New("db down")}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
}

// ─── List ──────────────────────────────────────────────────────────────────

func TestListForwardsFiltersAndPaging(t *testing.T) {
	tenant := uuid.New()
	store := &recordingStore{listNext: "cursor-2"}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	resp, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{
		TenantId:      tenant.String(),
		PrincipalKind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		Subject:       "agent-1",
		Dispositions: []adminv1.CapabilityDisposition{
			adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED,
			adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED,
		},
		SortOrder: commonv1.SortOrder_SORT_ORDER_DESC,
		PageToken: "cursor-1",
		PageSize:  25,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.listArgs == nil {
		t.Fatal("ListRecords was not called")
	}
	want := limes.ListRecordsRequest{
		TenantID:      tenant,
		PrincipalType: limes.PrincipalTypeAgent,
		Subject:       "agent-1",
		Dispositions:  []limes.RecordDisposition{limes.RecordDispositionExpired, limes.RecordDispositionRevoked},
		Order:         limes.ListOrderDescending,
		Cursor:        "cursor-1",
		Limit:         25,
	}
	if diff := cmp.Diff(want, *store.listArgs); diff != "" {
		t.Errorf("ListRecords request (-want +got):\n%s", diff)
	}
	if resp.GetNextPageToken() != "cursor-2" {
		t.Errorf("next token = %q", resp.GetNextPageToken())
	}
}

// A tenant alone lists every one of its capabilities' principals, oldest
// first.
func TestListATenantAlone(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	if _, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String()}); err != nil {
		t.Fatalf("List: %v", err)
	}
	a := store.listArgs
	if a.PrincipalType != limes.PrincipalTypeUnspecified || a.Subject != "" || a.Order != limes.ListOrderAscending {
		t.Errorf("a tenant alone listed %+v", a)
	}
}

// A request naming no dispositions lists the active capabilities, and those
// the deprecated include flags add, as List did before it took dispositions.
func TestListDispositionsFromTheIncludeFlags(t *testing.T) {
	active, expired, revoked := limes.RecordDispositionActive, limes.RecordDispositionExpired, limes.RecordDispositionRevoked
	cases := map[string]struct {
		expired, revoked bool
		want             []limes.RecordDisposition
	}{
		"neither": {want: []limes.RecordDisposition{active}},
		"expired": {expired: true, want: []limes.RecordDisposition{active, expired}},
		"revoked": {revoked: true, want: []limes.RecordDisposition{active, revoked}},
		"both":    {expired: true, revoked: true, want: []limes.RecordDisposition{active, expired, revoked}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			//nolint:staticcheck // SA1019: the deprecated flags are what this pins
			got := listDispositions(&adminv1.CapabilityServiceListRequest{IncludeExpired: tc.expired, IncludeRevoked: tc.revoked})
			if !slices.Equal(got, tc.want) {
				t.Errorf("dispositions = %v, want %v", got, tc.want)
			}
		})
	}
	named := listDispositions(&adminv1.CapabilityServiceListRequest{
		IncludeExpired: true, //nolint:staticcheck // SA1019: as above
		Dispositions:   []adminv1.CapabilityDisposition{adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED},
	})
	if !slices.Equal(named, []limes.RecordDisposition{revoked}) {
		t.Errorf("named dispositions = %v, want only revoked: they replace the flags", named)
	}
}

// Each capability comes back with the disposition limes listed it under.
func TestListGivesEachCapabilityItsDisposition(t *testing.T) {
	dispositions := map[limes.RecordDisposition]adminv1.CapabilityDisposition{
		limes.RecordDispositionActive:  adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE,
		limes.RecordDispositionExpired: adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED,
		limes.RecordDispositionRevoked: adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED,
	}
	store := &recordingStore{}
	for d := range dispositions {
		store.listOut = append(store.listOut, limes.RecordListing{
			Record:      limes.Record{Capability: limes.Capability{ID: uuid.New()}},
			Disposition: d,
		})
	}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	resp, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String()})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.GetCapabilities()) != len(store.listOut) {
		t.Fatalf("listed %d, want %d", len(resp.GetCapabilities()), len(store.listOut))
	}
	for i, c := range resp.GetCapabilities() {
		in := store.listOut[i]
		if c.GetId() != in.Record.Capability.ID.String() || c.GetDisposition() != dispositions[in.Disposition] {
			t.Errorf("capability %d = %s %v, want %s %v", i, c.GetId(), c.GetDisposition(), in.Record.Capability.ID, dispositions[in.Disposition])
		}
	}
}

// Every disposition maps both ways, so none is listed as unspecified.
func TestDispositionsRoundTrip(t *testing.T) {
	for _, d := range []limes.RecordDisposition{limes.RecordDispositionActive, limes.RecordDispositionExpired, limes.RecordDispositionRevoked} {
		p := dispositionToProto(d)
		if p == adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_UNSPECIFIED || protoToDisposition(p) != d {
			t.Errorf("%q maps to %v and back to %q", d, p, protoToDisposition(p))
		}
	}
}

// Without a record lister List says so rather than list nothing, which
// would read as "this tenant holds no capabilities".
func TestListUnwiredIsUnavailable(t *testing.T) {
	m := newMinter(t, &fakeStore{})
	h := NewHandler(m.Issuer, m.Verifier, &fakeStore{}, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String()})
	if codeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", codeOf(err))
	}
}

func TestListEmptyIsNotNil(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	resp, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp.GetCapabilities() == nil {
		t.Error("an empty list must serialise as [], not null")
	}
}

func TestListIsCedarGated(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &denyAuthorizer{})

	_, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestListRejectsBadTenant(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: "nope"})
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestListStoreErrorIsInternal(t *testing.T) {
	store := &recordingStore{listErr: errors.New("db down")}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(),
	})
	if codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
}

// A page token the store cannot read is a bad request, not a server fault.
func TestListBadPageTokenIsInvalidArgument(t *testing.T) {
	store := &recordingStore{listErr: fmt.Errorf("%w: cursor", limes.ErrInvalidRequest)}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(), PageToken: "garbage",
	})
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

// ─── GetUsage ──────────────────────────────────────────────────────────────

// An unwired counter store must be visibly Unavailable rather than reporting
// zero usage, which would read as "this capability spent nothing".
func TestGetUsageUnwiredIsUnavailable(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", codeOf(err))
	}
}

func TestGetUsageReturnsCounters(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{}
	usage := &fakeUsage{out: meter.Usage{
		CapabilityID: id, RequestCount: 7, SpentAmount: limes.MustParseAmount("42"), UnitCode: "EUR",
	}}
	h := mkHandler(t, store, usage, &allowAuthorizer{})

	resp, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{
		Id: id.String(),
	})
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if usage.got != id {
		t.Errorf("looked up %v, want %v", usage.got, id)
	}
	if resp.GetRequestCount() != 7 || !proto.Equal(resp.GetSpent(), &money.Money{CurrencyCode: "EUR", Units: 42}) {
		t.Errorf("counters = %d / %v", resp.GetRequestCount(), resp.GetSpent())
	}
}

// A row in no unit is answered in apiutil.NoUnit, so clients never render a
// bare number and none is passed off as the platform's currency.
func TestGetUsageAnswersNoUnitAsNoUnit(t *testing.T) {
	store := &fakeStore{}
	usage := &fakeUsage{out: meter.Usage{CapabilityID: uuid.New()}}
	h := mkHandler(t, store, usage, &allowAuthorizer{})

	resp, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if got := resp.GetSpent().GetCurrencyCode(); got != apiutil.NoUnit {
		t.Errorf("unit = %q, want %q", got, apiutil.NoUnit)
	}
}

func TestGetUsageNotFound(t *testing.T) {
	store := &fakeStore{}
	usage := &fakeUsage{err: meter.ErrUsageNotFound}
	h := mkHandler(t, store, usage, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
}

func TestGetUsageStoreErrorIsInternal(t *testing.T) {
	store := &fakeStore{}
	usage := &fakeUsage{err: errors.New("db down")}
	h := mkHandler(t, store, usage, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
}

func TestGetUsageRejectsBadID(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, &fakeUsage{}, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{Id: "nope"})
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestGetUsageIsCedarGated(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, &fakeUsage{}, &denyAuthorizer{})

	_, err := h.GetUsage(adminCtx(), &adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// ─── cross-tenant issuance ─────────────────────────────────────────────────

// tenantCtx is a caller scoped to one tenant with a tenant-level role — the
// shape that could previously mint for anybody.
func tenantCtx(tenant uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tenant-operator", TenantID: tenant, Roles: []string{"tenant.admin"},
	})
}

// Since ADR-0010 a capability authenticates AS its subject's tenant, so minting
// one for another tenant is a full cross-tenant grant. Cedar authorises
// IssueCapability against the caller's own tenant policy and says nothing
// about the subject's tenant, so the handler has to.
func TestIssue_NonPlatformAdminCannotIssueForAnotherTenant(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	caller, victim := uuid.New(), uuid.New()

	_, err := h.Issue(tenantCtx(caller), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: victim.String(), Subject: "victim",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	})

	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// Issuing within one's own tenant stays available to a tenant-level caller.
func TestIssue_TenantAdminMayIssueForItsOwnTenant(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	own := uuid.New()

	_, err := h.Issue(tenantCtx(own), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: own.String(), Subject: "self",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	})
	if err != nil {
		t.Fatalf("Issue for own tenant: %v", err)
	}
}

// A platform admin still mints for anybody — that is how a consumer serving
// many tenants gets per-tenant credentials. Through the real engine and an
// empty tenant policy: the grant has to come from the built-in policy.
func TestIssue_PlatformAdminMayIssueForAnotherTenant(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, cedartest.Engine(""))

	_, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "svc",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	})
	if err != nil {
		t.Fatalf("Issue as platform admin: %v", err)
	}
}

// issuerCtx is the narrow grant: may mint a capability for any tenant, and
// carries no other authority.
func issuerCtx(tenant uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "apikey:acme", TenantID: tenant,
		Roles: []string{"platform.capability-issuer"},
	})
}

// The whole point of the narrow role: a consumer serving many tenants mints
// per-tenant capabilities without holding platform.admin.
//
// Through the real engine, not a stub that allows everything: with the stub
// this test passed for as long as no policy granted the role anything, while
// every Issue it made in a cluster was denied.
func TestIssue_CapabilityIssuerMayIssueForAnotherTenant(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, cedartest.Engine(""))

	_, err := h.Issue(issuerCtx(uuid.New()), &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "svc",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	})
	if err != nil {
		t.Fatalf("Issue as capability-issuer: %v", err)
	}
}

// ─── another tenant's capability ───────────────────────────────────────────

// lookupStore records the context the owner lookup ran on.
type lookupStore struct {
	recordingStore
	// getCtxs are the contexts of every GetRecord, in order: the owner
	// lookup first, if there is one.
	getCtxs []context.Context
}

func (s *lookupStore) GetRecord(ctx context.Context, id uuid.UUID) (limes.Record, error) {
	s.getCtxs = append(s.getCtxs, ctx)
	return s.recordingStore.GetRecord(ctx, id)
}

func callerCtx(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "operator", TenantID: tenant, Roles: roles,
	})
}

// Revoke and GetUsage name a capability by id alone, and its rows are
// RLS-isolated by its own tenant. A platform admin's connection is scoped to
// the platform tenant, so before this both answered NotFound for every
// capability the admin had just issued to a tenant.
func TestRevokeAndGetUsageActOnTheCapabilitysTenant(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, limes.OpGet)

	// The roles are admitted by the built-in policy alone; the tenant-confined
	// caller holds no role, so its own tenant's policy has to grant it.
	const tenantGrant = `permit(principal, action in [Action::"RevokeCapability", Action::"ReadCapability"], resource);`
	cases := map[string]struct {
		ctx          context.Context
		tenantPolicy string
		wantActing   bool
	}{
		"platform admin":         {callerCtx(uuid.New(), "platform.admin"), "", true},
		"capability issuer":      {callerCtx(uuid.New(), "platform.capability-issuer"), "", true},
		"tenant-confined caller": {callerCtx(uuid.New()), tenantGrant, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
			usage := &fakeUsage{out: meter.Usage{CapabilityID: target.ID}}
			h := mkHandler(t, store, usage, cedartest.Engine(tc.tenantPolicy))

			if _, err := h.Revoke(tc.ctx, &adminv1.CapabilityServiceRevokeRequest{
				Id: target.ID.String(),
			}); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if _, err := h.GetUsage(tc.ctx, &adminv1.CapabilityServiceGetUsageRequest{
				Id: target.ID.String(),
			}); err != nil {
				t.Fatalf("GetUsage: %v", err)
			}

			for call, ctx := range map[string]context.Context{"Revoke": store.revokeCtx, "GetUsage": usage.ctx} {
				acting, ok := auth.ActingTenant(ctx)
				switch {
				case tc.wantActing && (!ok || acting != owner):
					t.Errorf("%s ran acting on %v (set=%v), want the owner %v", call, acting, ok, owner)
				case !tc.wantActing && ok:
					t.Errorf("%s acted on %v for a caller confined to its own tenant", call, acting)
				}
			}
			if tc.wantActing {
				if len(store.getCtxs) == 0 || !auth.CrossTenantRead(store.getCtxs[0]) {
					t.Error("the owner lookup must read across tenants, or RLS hides the record")
				}
			} else {
				for _, ctx := range store.getCtxs {
					if auth.CrossTenantRead(ctx) {
						t.Error("a tenant-confined caller must not get a cross-tenant owner lookup")
					}
				}
			}
		})
	}
}

// An id that matches no capability stays NotFound for an admin too, rather
// than turning into Internal on the owner lookup.
func TestRevokeUnknownIDIsNotFoundForAnAdmin(t *testing.T) {
	store := &recordingStore{revokeErr: limes.ErrNotFound}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

	_, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{
		Id: uuid.New().String(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
}
