package apiutil

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// What the authorization path admits is what the slot answers, each tenant
// once; a nil tenant, or a context without a slot, records nothing.
func TestAdmitTenant(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ctx := WithAdmissionSlot(context.Background())
	AdmitTenant(ctx, a)
	AdmitTenant(context.WithValue(ctx, auditAdmissionKey{}, (*auditAdmissionSlot)(nil)), b) // a nil slot
	AdmitTenant(ctx, uuid.Nil())
	AdmitTenant(ctx, a)
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	AdmitTenant(derived, b)
	if got, want := AdmittedTenants(ctx), []uuid.UUID{a, b}; !slices.Equal(got, want) {
		t.Errorf("admitted %v, want %v", got, want)
	}
	if got := AdmittedTenants(context.Background()); got != nil {
		t.Errorf("no slot admitted %v, want nil", got)
	}
	if got := AdmittedTenants(context.WithValue(ctx, auditAdmissionKey{}, (*auditAdmissionSlot)(nil))); got != nil {
		t.Errorf("a nil slot admitted %v, want nil", got)
	}
	AdmitTenant(context.Background(), a) // no slot: must not panic
}

// A decision admits the call to the resource's tenant only when it allows.
func TestDecideAdmitsTheTenantItAllowed(t *testing.T) {
	tenant := uuid.New()
	for _, tc := range []struct {
		name   string
		policy *recordingPolicy
		want   []uuid.UUID
	}{
		{"allowed", &recordingPolicy{decision: cedar.DecisionAllow}, []uuid.UUID{tenant}},
		{"denied", &recordingPolicy{decision: cedar.DecisionDeny}, nil},
		{"engine failed", &recordingPolicy{decision: cedar.DecisionAllow, err: errors.New("down")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithAdmissionSlot(context.Background())
			_ = Decide(ctx, tc.policy, &cedar.Principal{}, cedar.ActionInspectPolicy, &cedar.Resource{TenantID: tenant}, cedar.RequestContext{})
			if got := AdmittedTenants(ctx); !slices.Equal(got, tc.want) {
				t.Errorf("admitted %v, want %v", got, tc.want)
			}
		})
	}
}
