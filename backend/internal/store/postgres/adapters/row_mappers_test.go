package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// Row mappers are long struct literals over same-typed fields, which is the
// one shape where a transposition survives the compiler, the linter and a
// reading. Each fixture below gives every field of a given type a value no
// other field of that type has, so a swapped pair changes an assertion.

// quotaFromSQLC maps eight adjacent int64 columns: four limits and four
// usages. Enforcement compares one against the other, so a transposition
// does not fail — it changes what the system allows. Distinct powers of two
// make any pairing visible.
func TestQuotaFromSQLC(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	tenant := uuid.MustParse("7ba7b810-9dad-11d1-80b4-00c04fd430c8")
	reset := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	updated := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)

	got := quotaFromSQLC(sqlc.Quota{
		ID:                pgUUID(id),
		TenantID:          pgUUID(tenant),
		MaxTotalBytes:     1 << 10,
		MaxObjectCount:    1 << 11,
		MaxBytesPerDay:    1 << 12,
		MaxObjectsPerDay:  1 << 13,
		UsageTotalBytes:   1 << 14,
		UsageObjectCount:  1 << 15,
		UsageBytesToday:   1 << 16,
		UsageObjectsToday: 1 << 17,
		LastResetAt:       pgTS(reset),
		ResourceVersion:   11,
		UpdatedAt:         pgTS(updated),
	}, "backend-name", "bucket-name")

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"QuotaID", got.QuotaID, id},
		{"TenantID", got.TenantID, tenant},
		{"BackendID", got.BackendID, "backend-name"},
		{"BucketName", got.BucketName, "bucket-name"},
		{"MaxTotalBytes", got.MaxTotalBytes, int64(1 << 10)},
		{"MaxObjectCount", got.MaxObjectCount, int64(1 << 11)},
		{"MaxBytesPerDay", got.MaxBytesPerDay, int64(1 << 12)},
		{"MaxObjectsPerDay", got.MaxObjectsPerDay, int64(1 << 13)},
		{"UsageTotalBytes", got.UsageTotalBytes, int64(1 << 14)},
		{"UsageObjectCount", got.UsageObjectCount, int64(1 << 15)},
		{"UsageBytesToday", got.UsageBytesToday, int64(1 << 16)},
		{"UsageObjectsToday", got.UsageObjectsToday, int64(1 << 17)},
		{"ResourceVersion", got.ResourceVersion, int64(11)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if got.LastResetAt == nil || !got.LastResetAt.Equal(reset) {
		t.Errorf("LastResetAt = %v, want %v", got.LastResetAt, reset)
	}
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updated)
	}

	// A quota that has never been reset has no reset instant; the zero time
	// would read as "reset at the beginning of the epoch", which is a date,
	// not an absence.
	if got := quotaFromSQLC(sqlc.Quota{}, "b", "n"); got.LastResetAt != nil {
		t.Errorf("LastResetAt on an unreset quota = %v, want nil", got.LastResetAt)
	}
}

// versionFromSQLC carries three *string columns through derefStr and two
// JSONB columns through decodeMap. A version whose tags are served as its
// metadata is wrong on every read of that object.
func TestVersionFromSQLC(t *testing.T) {
	versionID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	objectID := uuid.MustParse("7ba7b810-9dad-11d1-80b4-00c04fd430c8")
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	retain := time.Date(2027, 2, 3, 4, 5, 6, 0, time.UTC)

	etag, checksum, contentType := "etag-value", "checksum-value", "text/csv"
	size := int64(4096)

	got := versionFromSQLC(sqlc.ObjectVersion{
		ID:                pgUUID(versionID),
		ObjectID:          pgUUID(objectID),
		IsDeleteMarker:    true,
		StoragePath:       "tenant/coll/key",
		SizeBytes:         &size,
		Etag:              &etag,
		ChecksumAlgorithm: checksumAlgoInt("SHA256"),
		Checksum:          &checksum,
		ContentType:       &contentType,
		Metadata:          []byte(`{"origin":"metadata"}`),
		Tags:              []byte(`{"origin":"tags"}`),
		CreatedAt:         pgTS(created),
	}, "COMPLIANCE", pgTS(retain), true)

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"VersionID", got.VersionID, versionID},
		{"ObjectID", got.ObjectID, objectID},
		{"IsDeleteMarker", got.IsDeleteMarker, true},
		{"StoragePath", got.StoragePath, "tenant/coll/key"},
		{"SizeBytes", got.SizeBytes, int64(4096)},
		{"ETag", got.ETag, "etag-value"},
		{"ChecksumAlgo", got.ChecksumAlgo, "SHA256"},
		{"Checksum", got.Checksum, "checksum-value"},
		{"ContentType", got.ContentType, "text/csv"},
		{"LockMode", got.LockMode, "COMPLIANCE"},
		{"LegalHold", got.LegalHold, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if got.Metadata["origin"] != "metadata" {
		t.Errorf("Metadata = %v, want origin=metadata", got.Metadata)
	}
	if got.Tags["origin"] != "tags" {
		t.Errorf("Tags = %v, want origin=tags", got.Tags)
	}
	if got.LockRetainUntil == nil || !got.LockRetainUntil.Equal(retain) {
		t.Errorf("LockRetainUntil = %v, want %v", got.LockRetainUntil, retain)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	// A NULL size is 0 rather than a panic, and a version with no retention
	// has none — a zero instant would read as retention that has expired.
	bare := versionFromSQLC(sqlc.ObjectVersion{}, "", pgtype.Timestamptz{}, false)
	if bare.SizeBytes != 0 {
		t.Errorf("SizeBytes on a NULL column = %d, want 0", bare.SizeBytes)
	}
	if bare.LockRetainUntil != nil {
		t.Errorf("LockRetainUntil with no retention = %v, want nil", bare.LockRetainUntil)
	}
}

// collectionFromSQLC takes the backend and bucket names as two adjacent
// string parameters. Swapped, a collection reports a binding that resolves
// to nothing — and the caller has no second source to check it against.
func TestCollectionFromSQLC(t *testing.T) {
	tenant := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 7, 8, 9, 10, 11, 0, time.UTC)

	got := collectionFromSQLC(sqlc.Collection{
		TenantID:        pgUUID(tenant),
		Name:            "collection-name",
		DisplayName:     "display-name",
		CedarPolicy:     "permit(principal, action, resource);",
		LifecycleRules:  []byte(`[{"id":"expire"}]`),
		ResourceVersion: 5,
		CreatedAt:       pgTS(created),
		UpdatedAt:       pgTS(updated),
	}, "backend-name", "bucket-name")

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"TenantID", got.TenantID, tenant},
		{"Collection", got.Collection, "collection-name"},
		{"DisplayName", got.DisplayName, "display-name"},
		{"BackendID", got.BackendID, "backend-name"},
		{"BucketName", got.BucketName, "bucket-name"},
		{"CedarPolicy", got.CedarPolicy, "permit(principal, action, resource);"},
		{"ResourceVersion", got.ResourceVersion, int64(5)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if string(got.LifecycleRules) != `[{"id":"expire"}]` {
		t.Errorf("LifecycleRules = %s", got.LifecycleRules)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps = (%v, %v), want (%v, %v)", got.CreatedAt, got.UpdatedAt, created, updated)
	}
}
