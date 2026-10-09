package operationh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// TestOperationRPCsAssertCapabilityOp pins the op each RPC requires of a
// capability, and that a refusal lands before Cedar and the repository.
func TestOperationRPCsAssertCapabilityOp(t *testing.T) {
	t.Parallel()

	tid := uuid.New()
	opID := uuid.New()
	okRepo := func() *fakeRepo {
		return &fakeRepo{
			getFn: func(context.Context, uuid.UUID, uuid.UUID) (Operation, error) {
				return Operation{OperationID: opID, TenantID: tid}, nil
			},
			cancelFn: func(context.Context, uuid.UUID, uuid.UUID) error { return nil },
			listFn: func(context.Context, uuid.UUID, *State, uuid.UUID, int32, string, bool) ([]Operation, string, error) {
				return nil, "", nil
			},
		}
	}
	calls := map[string]func(*Handler, context.Context) error{
		"GetOperation": func(h *Handler, ctx context.Context) error {
			_, err := h.GetOperation(ctx, opID)
			return err
		},
		"CancelOperation": func(h *Handler, ctx context.Context) error {
			return h.CancelOperation(ctx, opID)
		},
		"ListOperations": func(h *Handler, ctx context.Context) error {
			_, _, err := h.ListOperations(ctx, nil, 0, "", "", false)
			return err
		},
	}

	cases := []struct {
		name     string
		rpc      string
		caveats  limes.Caveats
		wantCode connect.Code // zero: allowed
	}{
		{"get with get", "GetOperation", limes.Caveats{Ops: []limes.Op{limes.OpGet}}, 0},
		{"get without get", "GetOperation", limes.Caveats{Ops: []limes.Op{limes.OpList}}, connect.CodePermissionDenied},
		{"list with list", "ListOperations", limes.Caveats{Ops: []limes.Op{limes.OpList}}, 0},
		{"list without list", "ListOperations", limes.Caveats{Ops: []limes.Op{limes.OpGet}}, connect.CodePermissionDenied},
		{"cancel with manage", "CancelOperation", limes.Caveats{Ops: []limes.Op{limes.OpManage}}, 0},
		{"cancel with delete only", "CancelOperation", limes.Caveats{Ops: []limes.Op{limes.OpDelete}}, connect.CodePermissionDenied},
		{
			"resource-restricted capability refused",
			"GetOperation",
			limes.Caveats{Ops: []limes.Op{limes.OpGet}, ResourcePrefixes: []string{"paladin://t/c"}},
			connect.CodePermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := okRepo()
			az := allowAuthorizer()
			ctx := auth.WithCapability(authedCtx(tid), &limes.Capability{Caveats: tc.caveats})
			err := calls[tc.rpc](NewHandler(fr, az), ctx)
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			wantCode(t, err, tc.wantCode)
			if len(az.calls) != 0 {
				t.Fatalf("Cedar consulted %d time(s) after the capability refused", len(az.calls))
			}
		})
	}
}
