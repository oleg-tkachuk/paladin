package capabilityh

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeCopyUsage serves fixed counters and records what it was asked. It
// refuses a request a store refuses, as every store does.
type fakeCopyUsage struct {
	out []meter.CopyUsage
	err error
	ids [][]byte
	cap uuid.UUID
	ctx context.Context
}

func (f *fakeCopyUsage) CopyUsage(ctx context.Context, req meter.CopyUsageRequest) (meter.CopyUsageResult, error) {
	if err := req.Validate(); err != nil {
		return meter.CopyUsageResult{}, err
	}
	f.ids, f.cap, f.ctx = req.RevocationIDs, req.CapabilityID, ctx
	return meter.CopyUsageResult{Copies: f.out}, f.err
}

// The limits two attenuations set on a copy, the outer first, and the
// counters the outer one has.
const (
	copyUnit            = "EUR"
	innerMaxRequests    = 10
	outerBudgetUnits    = 2
	outerRequestCount   = 7
	outerSpentAmount    = "1.25"
	outerReservedAmount = "0.5"
)

func biscuitUsageReq() *adminv1.CapabilityServiceGetBiscuitUsageRequest {
	return &adminv1.CapabilityServiceGetBiscuitUsageRequest{Token: "biscuit"}
}

// Every limit in force is reported, innermost first, with its counters; a
// limit never used reports zero; the unit is the capability's.
func TestGetBiscuitUsageReportsEachLimit(t *testing.T) {
	owner := uuid.New()
	outerBudget := limes.BudgetCeiling{Max: outerBudgetUnits * limes.NanosPerUnit}
	innerRequests := limes.RequestCeiling{Max: innerMaxRequests}
	target, copied := mintCopy(t, owner, limes.Capability{Caveats: limes.Caveats{Budget: limes.BudgetCeiling{UnitCode: copyUnit}}},
		limes.Attenuation{Budget: &outerBudget}, limes.Attenuation{Requests: &innerRequests})
	limits := copied.Copies()
	if len(limits) != 2 {
		t.Fatalf("copy limits = %v, want the two attenuations'", limits)
	}
	inner, outer := limits[0].RevocationID, limits[1].RevocationID
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	copier := &fakeCopier{copy: copied}
	reader := &fakeCopyUsage{out: []meter.CopyUsage{{
		RevocationID: outer, CapabilityID: target.ID, RequestCount: outerRequestCount,
		SpentAmount: limes.MustParseAmount(outerSpentAmount), ReservedAmount: limes.MustParseAmount(outerReservedAmount),
	}}}
	h := mkHandler(t, store, nil, &allowAuthorizer{}).
		WithBiscuitCopies(copier, &copyStore{}).WithCopyUsage(reader)

	resp, err := h.GetBiscuitUsage(callerCtx(uuid.New(), "platform.admin"), biscuitUsageReq())
	if err != nil {
		t.Fatalf("GetBiscuitUsage: %v", err)
	}
	if resp.GetCapabilityId() != target.ID.String() {
		t.Errorf("capability %q", resp.GetCapabilityId())
	}
	// The counters are read under the copy's own capability: a copy's
	// revocation id alone can turn up on another capability's chain.
	if reader.cap != target.ID {
		t.Errorf("copy usage read under capability %s, want %s", reader.cap, target.ID)
	}
	got := resp.GetCopies()
	if len(got) != 2 {
		t.Fatalf("copies = %v", got)
	}
	in, out := got[0], got[1]
	if !bytes.Equal(in.GetRevocationId(), inner) || in.GetMaxRequests() != innerMaxRequests || in.GetRequestCount() != 0 {
		t.Errorf("innermost = %v", in)
	}
	if !bytes.Equal(out.GetRevocationId(), outer) || out.GetRequestCount() != outerRequestCount ||
		!proto.Equal(out.GetMaxBudget(), &money.Money{CurrencyCode: copyUnit, Units: outerBudgetUnits}) ||
		!proto.Equal(out.GetSpent(), &money.Money{CurrencyCode: copyUnit, Units: 1, Nanos: 250_000_000}) ||
		!proto.Equal(out.GetReserved(), &money.Money{CurrencyCode: copyUnit, Nanos: 500_000_000}) {
		t.Errorf("outermost = %v", out)
	}
	// Each block set one axis; the other is absent, not a limit of 0.
	if in.MaxBudget != nil || out.MaxRequests != nil {
		t.Errorf("an axis a block left alone reads as a limit: inner budget %v, outer requests %v", in.GetMaxBudget(), out.MaxRequests)
	}
	if len(reader.ids) != 2 {
		t.Errorf("reader asked for %q", reader.ids)
	}
	if acting, ok := auth.ActingTenant(reader.ctx); !ok || acting != owner {
		t.Errorf("read acting on %v (set=%v), want the owner %v", acting, ok, owner)
	}
}

func TestGetBiscuitUsageRefusals(t *testing.T) {
	// A copy with a limit of its own, so the reader is asked about it.
	limit := limes.RequestCeiling{Max: innerMaxRequests}
	visible, good := mintCopy(t, uuid.New(), limes.Capability{}, limes.Attenuation{Requests: &limit})
	cases := map[string]struct {
		authz  cedar.Authorizer
		copier BiscuitCopier
		reader *fakeCopyUsage
		record *limes.Capability
		want   connect.Code
	}{
		"Cedar denies":           {&denyAuthorizer{}, &fakeCopier{copy: good}, &fakeCopyUsage{}, &visible, connect.CodePermissionDenied},
		"not wired":              {&allowAuthorizer{}, &fakeCopier{copy: good}, nil, &visible, connect.CodeUnavailable},
		"not a genuine Biscuit":  {&allowAuthorizer{}, &fakeCopier{err: limes.ErrInvalidToken}, &fakeCopyUsage{}, &visible, connect.CodeInvalidArgument},
		"capability not visible": {&allowAuthorizer{}, &fakeCopier{copy: good}, &fakeCopyUsage{}, nil, connect.CodeNotFound},
		"reader fails":           {&allowAuthorizer{}, &fakeCopier{copy: good}, &fakeCopyUsage{err: errors.New("db down")}, &visible, connect.CodeInternal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingStore{fakeStore: fakeStore{cap: tc.record}}
			h := mkHandler(t, store, nil, tc.authz).WithBiscuitCopies(tc.copier, &copyStore{})
			if tc.reader != nil {
				h.WithCopyUsage(tc.reader)
			}
			_, err := h.GetBiscuitUsage(callerCtx(uuid.New()), biscuitUsageReq())
			if codeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v (%v)", codeOf(err), tc.want, err)
			}
		})
	}
}

// A copy frozen at 0 reports its limits as 0, distinct from a copy that set
// none.
func TestGetBiscuitUsageReportsAFrozenCopy(t *testing.T) {
	zeroRequests, zeroBudget := limes.RequestCeiling{}, limes.BudgetCeiling{}
	target, copied := mintCopy(t, uuid.New(), limes.Capability{Caveats: limes.Caveats{Budget: limes.BudgetCeiling{UnitCode: copyUnit}}},
		limes.Attenuation{Requests: &zeroRequests, Budget: &zeroBudget})
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	h := mkHandler(t, store, nil, &allowAuthorizer{}).
		WithBiscuitCopies(&fakeCopier{copy: copied}, &copyStore{}).WithCopyUsage(&fakeCopyUsage{})

	resp, err := h.GetBiscuitUsage(callerCtx(uuid.New(), "platform.admin"), biscuitUsageReq())
	if err != nil {
		t.Fatalf("GetBiscuitUsage: %v", err)
	}
	got := resp.GetCopies()
	if len(got) != 1 || got[0].MaxRequests == nil || got[0].GetMaxRequests() != 0 ||
		!proto.Equal(got[0].GetMaxBudget(), &money.Money{CurrencyCode: copyUnit}) {
		t.Errorf("copies = %v, want one frozen at 0 requests and 0 %s", got, copyUnit)
	}
}
