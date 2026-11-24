package tenanth

import (
	"context"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
)

// The version a test cancels at.
const cancelVersion = int64(3)

func TestCancelTenantStorageMigration(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CancelTenantStorageMigration(principalCtx(tid), tid, cancelVersion)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied, nothing cancelled", func(t *testing.T) {
		fr := &fakeRepo{cancelMigrationFn: func(context.Context, uuid.UUID, int64, string) (StorageMigration, error) {
			t.Error("a migration was cancelled past a denying policy")
			return StorageMigration{}, nil
		}}
		_, err := NewHandler(fr, deny()).CancelTenantStorageMigration(adminCtx(tid), tid, cancelVersion)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("no version → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CancelTenantStorageMigration(adminCtx(tid), tid, 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("cancelled: the version and who asked reach the repository", func(t *testing.T) {
		var gotVersion int64
		var gotReason string
		fr := &fakeRepo{cancelMigrationFn: func(_ context.Context, id uuid.UUID, v int64, reason string) (StorageMigration, error) {
			if id != tid {
				t.Errorf("cancelled tenant %s, want %s", id, tid)
			}
			gotVersion, gotReason = v, reason
			return StorageMigration{TenantID: id, State: migstate.RollingBack, Error: reason}, nil
		}}
		m, err := NewHandler(fr, allow()).CancelTenantStorageMigration(adminCtx(tid), tid, cancelVersion)
		if err != nil {
			t.Fatal(err)
		}
		if gotVersion != cancelVersion {
			t.Errorf("version %d, want %d", gotVersion, cancelVersion)
		}
		if want := fmt.Sprintf(reasonCancelledBy, "u1"); gotReason != want || m.Error != want {
			t.Errorf("reason %q (status %q), want %q", gotReason, m.Error, want)
		}
	})

	for name, tc := range map[string]struct {
		err  error
		want connect.Code
	}{
		"not cancellable → failed precondition": {fmt.Errorf("%w (state %q)", ErrStorageMigrationNotCancellable, migstate.Cleaned), connect.CodeFailedPrecondition},
		"stale version → aborted":               {ErrVersionMismatch, connect.CodeAborted},
		"none → not found":                      {ErrStorageMigrationNotFound, connect.CodeNotFound},
		"store down → internal":                 {errStoreDown, connect.CodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			fr := &fakeRepo{cancelMigrationFn: func(context.Context, uuid.UUID, int64, string) (StorageMigration, error) {
				return StorageMigration{}, tc.err
			}}
			_, err := NewHandler(fr, allow()).CancelTenantStorageMigration(adminCtx(tid), tid, cancelVersion)
			wantCode(t, err, tc.want)
		})
	}
}
