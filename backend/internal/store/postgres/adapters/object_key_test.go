package adapters

import (
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// The two inserts every object row goes through refuse a key the composer
// would refuse, before they touch the database: a caller that skipped the
// boundary check fails closed. Neither repository has a pool here, so a
// refusal that came after the transaction began would panic instead.
func TestObjectInsertsRefuseANonCanonicalKey(t *testing.T) {
	tenant := uuid.New()
	for _, key := range []string{"a//b", "..", "/a", "dir/", ""} {
		_, err := NewObjectRepo(nil, nil).CreateObject(t.Context(), objecth.CreateObjectArgs{
			TenantID: tenant, Collection: "docs", Key: key,
		})
		if !errors.Is(err, storagepath.ErrNotCanonical) {
			t.Errorf("CreateObject(%q) = %v, want a key refusal", key, err)
		}
		_, err = NewMultipartRepo(nil, nil).InitiateSession(t.Context(), multiparth.InitiateArgs{
			TenantID: tenant, Collection: "docs", Key: key,
		}, uuid.New(), "upload", "backend", "bucket")
		if !errors.Is(err, storagepath.ErrNotCanonical) {
			t.Errorf("InitiateSession(%q) = %v, want a key refusal", key, err)
		}
	}
}
