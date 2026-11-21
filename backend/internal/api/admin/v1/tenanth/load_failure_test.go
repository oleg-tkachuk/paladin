package tenanth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
)

// errStoreDown stands in for a store that could not be read: a dropped
// connection, a timeout.
var errStoreDown = errors.New("conn closed")

// Reading the tenant an RPC names: one that is not there is NOT_FOUND in the
// domain's words; a store that fails is the server's fault, never NOT_FOUND,
// which a client acts on by giving up on the tenant.
func TestTenantLoadFailures(t *testing.T) {
	tid := uuid.New()
	rpcs := map[string]func(h *Handler) error{
		"GetTenant": func(h *Handler) error { _, err := h.GetTenant(adminCtx(tid), tid); return err },
		"PreviewPurgeTenant": func(h *Handler) error {
			_, err := h.PreviewPurgeTenant(adminCtx(tid), tid)
			return err
		},
		"PurgeTenant": func(h *Handler) error { return h.PurgeTenant(adminCtx(tid), tid) },
		"GetTenantStorageMigration": func(h *Handler) error {
			_, err := h.GetTenantStorageMigration(adminCtx(tid), tid)
			return err
		},
	}
	for _, tc := range []struct {
		name    string
		repoErr error
		want    connect.Code
		message string
	}{
		{"missing", ErrNotFound, connect.CodeNotFound, ErrNotFound.Error()},
		{"store down", errStoreDown, connect.CodeInternal, ""},
	} {
		for rpc, call := range rpcs {
			t.Run(tc.name+"/"+rpc, func(t *testing.T) {
				fr := &fakeRepo{
					getFn:          func(context.Context, uuid.UUID) (Tenant, error) { return Tenant{}, tc.repoErr },
					getMigrationFn: func(context.Context, uuid.UUID) (StorageMigration, error) { return StorageMigration{}, tc.repoErr },
				}
				err := call(NewHandler(fr, allow()))
				wantCode(t, err, tc.want)
				var cerr *connect.Error
				if tc.message != "" && (!errors.As(err, &cerr) || cerr.Message() != tc.message) {
					t.Errorf("message = %q, want %q", cerr.Message(), tc.message)
				}
				if !errors.Is(err, tc.repoErr) {
					t.Error("the store's error is not kept as the cause")
				}
			})
		}
	}
}
