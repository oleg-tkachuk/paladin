package tenanth

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
)

// The preview names a tenant's tokens, as much as the purge reaches, so it
// asks of the caller what PurgeTenant asks; it reads an active tenant as well
// as a trashed one, and hands the repository the contract's limit.
func TestPreviewPurgeTenant(t *testing.T) {
	tid := uuid.New()
	trashed := func(_ context.Context, id uuid.UUID) (Tenant, error) {
		return Tenant{TenantID: id, DeletedAt: time.Now()}, nil
	}

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{getFn: trashed}, allow()).PreviewPurgeTenant(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{getFn: trashed}, deny()).PreviewPurgeTenant(adminCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo miss → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID) (Tenant, error) {
			return Tenant{}, errors.New("no row")
		}}
		_, err := NewHandler(fr, allow()).PreviewPurgeTenant(adminCtx(tid), tid)
		wantCode(t, err, connect.CodeNotFound)
	})

	for name, get := range map[string]func(context.Context, uuid.UUID) (Tenant, error){
		"trashed": trashed,
		"active": func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id}, nil
		},
	} {
		t.Run(name+" tenant → the repository's preview", func(t *testing.T) {
			want := PurgePreview{APITokenCount: 3, CapabilityCount: 7, Blockers: PurgeBlockers{Users: 1}}
			var gotID uuid.UUID
			var gotNames int
			fr := &fakeRepo{getFn: get, previewPurgeFn: func(_ context.Context, id uuid.UUID, names int) (PurgePreview, error) {
				gotID, gotNames = id, names
				return want, nil
			}}
			got, err := NewHandler(fr, allow()).PreviewPurgeTenant(adminCtx(tid), tid)
			if err != nil {
				t.Fatal(err)
			}
			if got.APITokenCount != want.APITokenCount || got.CapabilityCount != want.CapabilityCount || got.Blockers != want.Blockers {
				t.Errorf("preview = %+v, want %+v", *got, want)
			}
			if gotID != tid || gotNames != PurgePreviewNames {
				t.Errorf("asked the repository for %s, %d names; want %s, %d", gotID, gotNames, tid, PurgePreviewNames)
			}
		})
	}
}

func TestPurgeBlockersNone(t *testing.T) {
	for _, tc := range []struct {
		b    PurgeBlockers
		want bool
	}{
		{PurgeBlockers{}, true},
		{PurgeBlockers{Collections: 1}, false},
		{PurgeBlockers{Buckets: 1}, false},
		{PurgeBlockers{Users: 1}, false},
	} {
		if got := tc.b.None(); got != tc.want {
			t.Errorf("%+v.None() = %v, want %v", tc.b, got, tc.want)
		}
	}
}
