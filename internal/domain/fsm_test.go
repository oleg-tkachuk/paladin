package domain_test

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObjectFSM(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name          string
		initialState  domain.ObjectStatus
		event         domain.ObjectEvent
		expectedState domain.ObjectStatus
		expectError   bool
	}{
		// Completion
		{"Pending -> Complete", domain.ObjectPending, domain.EventObjectUploadComplete, domain.ObjectComplete, false},
		{"Uploaded -> Complete", domain.ObjectUploaded, domain.EventObjectUploadComplete, domain.ObjectComplete, false},
		{"Complete -> Complete (Idempotent)", domain.ObjectComplete, domain.EventObjectUploadComplete, domain.ObjectComplete, false},

		// Soft Delete
		{"Pending -> SoftDelete", domain.ObjectPending, domain.EventObjectSoftDelete, domain.ObjectSoftDeleted, false},
		{"Complete -> SoftDelete", domain.ObjectComplete, domain.EventObjectSoftDelete, domain.ObjectSoftDeleted, false},
		{"SoftDeleted -> SoftDelete (Idempotent)", domain.ObjectSoftDeleted, domain.EventObjectSoftDelete, domain.ObjectSoftDeleted, false},

		// Restore
		{"SoftDeleted -> Restore", domain.ObjectSoftDeleted, domain.EventObjectRestore, domain.ObjectComplete, false},

		// Hard Delete
		{"Pending -> HardDelete", domain.ObjectPending, domain.EventObjectHardDelete, domain.ObjectHardDeleted, false},
		{"Complete -> HardDelete", domain.ObjectComplete, domain.EventObjectHardDelete, domain.ObjectHardDeleted, false},
		{"SoftDeleted -> HardDelete", domain.ObjectSoftDeleted, domain.EventObjectHardDelete, domain.ObjectHardDeleted, false},
		{"HardDeleted -> HardDelete (Idempotent)", domain.ObjectHardDeleted, domain.EventObjectHardDelete, domain.ObjectHardDeleted, false},

		// Invalid transitions
		{"HardDeleted -> SoftDelete (Error)", domain.ObjectHardDeleted, domain.EventObjectSoftDelete, domain.ObjectHardDeleted, true},
		{"HardDeleted -> Restore (Error)", domain.ObjectHardDeleted, domain.EventObjectRestore, domain.ObjectHardDeleted, true},
		{"Pending -> Restore (Error)", domain.ObjectPending, domain.EventObjectRestore, domain.ObjectPending, true},
		{"Complete -> Restore (Error)", domain.ObjectComplete, domain.EventObjectRestore, domain.ObjectComplete, true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := domain.NewObjectFSM(tt.initialState)

			err := sm.Fire(tt.event)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				state, _ := sm.State(ctx)
				assert.Equal(t, tt.expectedState, state)
			}
		})
	}
}

func TestMultipartFSM(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name          string
		initialState  domain.MultipartStatus
		event         domain.MultipartEvent
		expectedState domain.MultipartStatus
		expectError   bool
	}{
		{"Initiated -> Complete", domain.MultipartInitiated, domain.EventMultipartComplete, domain.MultipartCompleted, false},
		{"Initiated -> Abort", domain.MultipartInitiated, domain.EventMultipartAbort, domain.MultipartAborted, false},
		{"Initiated -> Expire", domain.MultipartInitiated, domain.EventMultipartExpire, domain.MultipartExpired, false},

		{"Completed -> Complete (Idempotent)", domain.MultipartCompleted, domain.EventMultipartComplete, domain.MultipartCompleted, false},
		{"Aborted -> Abort (Idempotent)", domain.MultipartAborted, domain.EventMultipartAbort, domain.MultipartAborted, false},

		{"Completed -> Abort (Error)", domain.MultipartCompleted, domain.EventMultipartAbort, domain.MultipartCompleted, true},
		{"Aborted -> Complete (Error)", domain.MultipartAborted, domain.EventMultipartComplete, domain.MultipartAborted, true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm := domain.NewMultipartFSM(tt.initialState)

			err := sm.Fire(tt.event)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				state, _ := sm.State(ctx)
				assert.Equal(t, tt.expectedState, state)
			}
		})
	}
}
