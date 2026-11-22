package admindomain

import (
	"testing"
	"uuid"
)

// An entry is filed in the tenant its resource names only when its actor was
// admitted there: its own tenant, or one its call's authorization path
// admitted it to. A name the actor merely wrote files nothing.
func TestTrailTenant(t *testing.T) {
	own, other, third := uuid.New(), uuid.New(), uuid.New()
	inOwn := "tenants/" + own.String() + "/policies/p"
	inOther := "storageBackends/primary/buckets/b/tenants/" + other.String() + "/collections/c"
	cases := []struct {
		name     string
		resource string
		actor    uuid.UUID
		admitted []uuid.UUID
		want     uuid.UUID
	}{
		{"its own tenant, nothing admitted", inOwn, own, nil, own},
		{"its own tenant, another admitted", inOwn, own, []uuid.UUID{other}, own},
		{"another tenant, nothing admitted", inOther, own, nil, uuid.Nil()},
		{"another tenant, only its own admitted", inOther, own, []uuid.UUID{own}, uuid.Nil()},
		{"another tenant, a third admitted", inOther, own, []uuid.UUID{third}, uuid.Nil()},
		{"another tenant, admitted", inOther, own, []uuid.UUID{own, other}, other},
		{"no actor, a tenant named", inOther, uuid.Nil(), nil, uuid.Nil()},
		{"no actor, the tenant admitted", inOther, uuid.Nil(), []uuid.UUID{other}, other},
		{"no tenant in the name", "storageBackends/primary", own, []uuid.UUID{own}, uuid.Nil()},
		{"a slug for a tenant", "tenants/platform", own, []uuid.UUID{own}, uuid.Nil()},
		{"no name", "", own, []uuid.UUID{own}, uuid.Nil()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TrailTenant(c.resource, c.actor, c.admitted); got != c.want {
				t.Errorf("TrailTenant(%q, %s, %v) = %s, want %s", c.resource, c.actor, c.admitted, got, c.want)
			}
			e := AuditEntry{ResourceName: c.resource, ActorTenantID: c.actor, AdmittedTenants: c.admitted}
			if got := e.ResourceTenant(); got != c.want {
				t.Errorf("ResourceTenant() = %s, want %s", got, c.want)
			}
		})
	}
}
