package eventingest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

type fixedTenantState struct {
	state auth.TenantState
	err   error
}

func (f fixedTenantState) TenantState(context.Context, uuid.UUID) (auth.TenantState, error) {
	return f.state, f.err
}

// A storage event of a tenant in the trash is settled without touching the
// object: the tenant is frozen, and the reconciler promotes its pending upload
// on restore. A state that cannot be read is retried, not dropped.
func TestHandle_TrashedTenant(t *testing.T) {
	sf := SubjectFields{TenantID: uuid.NewString(), Collection: "docs", Key: "a.txt"}
	for name, tc := range map[string]struct {
		tenants    fixedTenantState
		wantErr    bool
		wantLookup bool
	}{
		"in the trash": {fixedTenantState{state: auth.TenantTrashed}, false, false},
		"live":         {fixedTenantState{state: auth.TenantLive}, false, true},
		"unreadable":   {fixedTenantState{err: errors.New("down")}, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeLookup{}
			h := &PromoteHandler{Lookup: f, Logger: zap.NewNop(), Tenants: tc.tenants}
			err := h.Handle(context.Background(), CloudEvent{ID: "e", Type: EventTypeUploaded, SubjectFields: sf})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Handle = %v, want error=%v", err, tc.wantErr)
			}
			if f.lookupCalled != tc.wantLookup {
				t.Errorf("looked the object up = %v, want %v", f.lookupCalled, tc.wantLookup)
			}
		})
	}
}
