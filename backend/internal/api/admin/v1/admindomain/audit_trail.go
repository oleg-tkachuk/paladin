package admindomain

import (
	"slices"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// TrailTenant is the tenant whose trail an audit entry is filed in besides
// its actor's own: the tenant its resource name sits under, but only when
// the actor was admitted there — its own tenant, or one of admitted, the
// tenants its call's authorization path admitted it to (apiutil.AdmitTenant).
// Otherwise uuid.Nil: the name is request text, and an actor that was
// refused, or never admitted, files nothing in the tenant it names — the
// entry stays in its own trail, its resource name recording the target it
// tried. A name with no tenant, or a slug, is uuid.Nil too.
//
// Every audit row's resource tenant is decided here (AuditEntry.ResourceTenant).
func TrailTenant(resourceName string, actorTenant uuid.UUID, admitted []uuid.UUID) uuid.UUID {
	named, ok := apiutil.TenantInResourceName(resourceName)
	if !ok {
		return uuid.Nil()
	}
	if named != actorTenant && !slices.Contains(admitted, named) {
		return uuid.Nil()
	}
	return named
}
