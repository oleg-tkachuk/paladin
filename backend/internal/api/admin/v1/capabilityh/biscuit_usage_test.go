package capabilityh

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeCopyUsage serves fixed counters and records what it was asked.
type fakeCopyUsage struct {
	out []capability.CopyUsage
	err error
	ids [][]byte
	ctx context.Context
}

func (f *fakeCopyUsage) CopyUsage(ctx context.Context, ids [][]byte) ([]capability.CopyUsage, error) {
	f.ids, f.ctx = ids, ctx
	return f.out, f.err
}

func biscuitUsageReq() *connect.Request[adminv1.CapabilityServiceGetBiscuitUsageRequest] {
	return connect.NewRequest(&adminv1.CapabilityServiceGetBiscuitUsageRequest{Token: "biscuit"})
}

// Every limit in force is reported, innermost first, with its counters; a
// limit never used reports zero; the unit is the capability's.
func TestGetBiscuitUsageReportsEachLimit(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, capability.OpGet)
	target.Caveats.UnitCode = "EUR"
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	limits := []capability.CopyCeiling{
		{RevocationID: []byte("inner"), MaxRequests: 10},
		{RevocationID: []byte("outer"), MaxBudgetMicros: 2 * capability.MicrosPerUnit},
	}
	copier := &fakeCopier{copy: capability.BiscuitCopy{CapabilityID: target.ID, RevocationID: []byte("inner"), Limits: limits}}
	reader := &fakeCopyUsage{out: []capability.CopyUsage{
		{RevocationID: []byte("outer"), CapabilityID: target.ID, RequestCount: 7, SpentAmount: 1.25, ReservedAmount: 0.5},
	}}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{}).
		WithBiscuitCopies(copier, &copyStore{}).WithCopyUsage(reader)

	resp, err := h.GetBiscuitUsage(callerCtx(uuid.New(), "platform.admin"), biscuitUsageReq())
	if err != nil {
		t.Fatalf("GetBiscuitUsage: %v", err)
	}
	if resp.Msg.GetCapabilityId() != target.ID.String() || resp.Msg.GetUnitCode() != "EUR" {
		t.Errorf("capability %q unit %q", resp.Msg.GetCapabilityId(), resp.Msg.GetUnitCode())
	}
	got := resp.Msg.GetCopies()
	if len(got) != 2 {
		t.Fatalf("copies = %v", got)
	}
	in, out := got[0], got[1]
	if !bytes.Equal(in.GetRevocationId(), []byte("inner")) || in.GetMaxRequests() != 10 || in.GetRequestCount() != 0 {
		t.Errorf("innermost = %v", in)
	}
	if !bytes.Equal(out.GetRevocationId(), []byte("outer")) || out.GetMaxBudgetMicros() != 2*capability.MicrosPerUnit ||
		out.GetRequestCount() != 7 || out.GetSpentMicros() != 1_250_000 || out.GetReservedMicros() != 500_000 {
		t.Errorf("outermost = %v", out)
	}
	if len(reader.ids) != 2 {
		t.Errorf("reader asked for %q", reader.ids)
	}
	if acting, ok := auth.ActingTenant(reader.ctx); !ok || acting != owner {
		t.Errorf("read acting on %v (set=%v), want the owner %v", acting, ok, owner)
	}
}

func TestGetBiscuitUsageRefusals(t *testing.T) {
	visible := mkParent(uuid.New(), capability.OpGet)
	good := capability.BiscuitCopy{CapabilityID: visible.ID, RevocationID: []byte("x")}
	cases := map[string]struct {
		authz  cedar.Authorizer
		copier BiscuitCopier
		reader *fakeCopyUsage
		record *capability.Capability
		want   connect.Code
	}{
		"Cedar denies":           {&denyAuthorizer{}, &fakeCopier{copy: good}, &fakeCopyUsage{}, &visible, connect.CodePermissionDenied},
		"not wired":              {&allowAuthorizer{}, &fakeCopier{copy: good}, nil, &visible, connect.CodeUnavailable},
		"not a genuine Biscuit":  {&allowAuthorizer{}, &fakeCopier{err: capability.ErrInvalidSignature}, &fakeCopyUsage{}, &visible, connect.CodeInvalidArgument},
		"capability not visible": {&allowAuthorizer{}, &fakeCopier{copy: good}, &fakeCopyUsage{}, nil, connect.CodeNotFound},
		"reader fails":           {&allowAuthorizer{}, &fakeCopier{copy: good}, &fakeCopyUsage{err: errors.New("db down")}, &visible, connect.CodeInternal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingStore{fakeStore: fakeStore{cap: tc.record}}
			h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, tc.authz).WithBiscuitCopies(tc.copier, &copyStore{})
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
