package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/policies"
)

// Publishing makes data readable by anyone, so no tenant role holds it — not
// even under the default tenant policy every tenant is created with, whose
// roles administer a tenant's buckets and data (ADR-0027). Only the built-in
// grants, to platform.admin and platform.tenant-provisioner, carry it.
func TestNoTenantRoleMayPublish(t *testing.T) {
	e := NewEngine(fakeStore{text: policies.DefaultTenantPolicy}, time.Minute)
	tenant := uuid.New()
	for _, role := range []string{
		"tenant.admin", "bucket.admin", "compliance.officer", "policy.author", "secrets.rotator", "tenant.user",
	} {
		t.Run(role, func(t *testing.T) {
			dec, err := e.IsAuthorized(context.Background(),
				&Principal{Subject: "u@acme", TenantID: tenant, Roles: []string{role}},
				ActionConfigurePublicRead,
				&Resource{TenantID: tenant, Collection: "photos"},
				RequestContext{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if dec != DecisionDeny {
				t.Errorf("%s may publish under the default policy", role)
			}
		})
	}
}
