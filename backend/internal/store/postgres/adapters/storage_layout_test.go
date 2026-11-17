package adapters

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The handler's layout names are written to the tenant_storage_layout enum
// as they are, so they must spell its values; a drift fails every create.
func TestStorageLayoutsSpellTheEnum(t *testing.T) {
	for handler, enum := range map[string]sqlc.TenantStorageLayout{
		tenanth.StorageLayoutShared:    sqlc.TenantStorageLayoutShared,
		tenanth.StorageLayoutDedicated: sqlc.TenantStorageLayoutDedicated,
	} {
		if handler != string(enum) {
			t.Errorf("tenanth layout %q, enum %q", handler, enum)
		}
	}
}
