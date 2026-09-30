package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// decodeBucketRow takes 21 positional arguments and is called from three
// places that each spell all 21 out. Adjacent parameters share types — two
// strings for display name and region, four bools around versioning and
// replication, two more strings for the replication destination and its
// filter — so transposing a neighbouring pair compiles, passes review, and
// reports a bucket that keeps deletes forever as one with versioning on, or
// stores a replication filter where the destination bucket belongs.
//
// The fixture gives every field a value distinguishable from every other
// field of its type, so any transposition changes an assertion.

func wantBucket(t *testing.T, got admindomain.Bucket, owner uuid.UUID, created, updated time.Time) {
	t.Helper()
	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"BackendID", got.BackendID, "backend-name"},
		{"BucketName", got.BucketName, "bucket-name"},
		{"DisplayName", got.DisplayName, "display-name"},
		{"Region", got.Region, "eu-west-3"},
		{"OwnerTenantID", got.OwnerTenantID, owner},
		{"CedarPolicy", got.CedarPolicy, "permit(principal, action, resource);"},
		{"ObjectLock.Enabled", got.ObjectLock.Enabled, true},
		{"ObjectLock.DefaultMode", got.ObjectLock.DefaultMode, "COMPLIANCE"},
		{"ObjectLock.DefaultRetention", got.ObjectLock.DefaultRetention, 90 * time.Second},
		{"Versioning.Enabled", got.Versioning.Enabled, true},
		{"Versioning.KeepDeletesForever", got.Versioning.KeepDeletesForever, false},
		{"Replication.Enabled", got.Replication.Enabled, true},
		{"Replication.DestinationBucket", got.Replication.DestinationBucket, "dest-bucket"},
		{"Replication.Filter", got.Replication.Filter, "prefix/"},
		{"ProvisionState", got.ProvisionState, "pending"},
		{"ResourceVersion", got.ResourceVersion, int64(7)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if got.Labels["team"] != "storage" {
		t.Errorf("Labels = %v, want team=storage", got.Labels)
	}
	if got.Constraints.MaxObjectSizeBytes != 4096 || got.Constraints.RequiredChecksumAlgorithm != "SHA256" {
		t.Errorf("Constraints = %+v, want the 4096/SHA256 pair", got.Constraints)
	}
	if len(got.LifecycleRules) != 1 || got.LifecycleRules[0].ID != "expire-old" {
		t.Errorf("LifecycleRules = %+v, want one rule expire-old", got.LifecycleRules)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updated)
	}
}

func TestBucketRowMappers(t *testing.T) {
	owner := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)

	const (
		labels      = `{"team":"storage"}`
		lifecycle   = `[{"id":"expire-old"}]`
		constraints = `{"MaxObjectSizeBytes":4096,"RequiredChecksumAlgorithm":"SHA256"}`
	)

	get := sqlc.GetBucketV2Row{
		BackendName:                       "backend-name",
		Name:                              "bucket-name",
		DisplayName:                       "display-name",
		Region:                            "eu-west-3",
		Labels:                            []byte(labels),
		OwnerTenantID:                     pgUUID(owner),
		CedarPolicy:                       "permit(principal, action, resource);",
		Constraints:                       []byte(constraints),
		LifecycleRules:                    []byte(lifecycle),
		ObjectLockEnabled:                 true,
		ObjectLockDefaultMode:             lockModeToSQL("COMPLIANCE"),
		ObjectLockDefaultRetentionSeconds: 90,
		VersioningEnabled:                 true,
		VersioningKeepDeletesForever:      false,
		ReplicationEnabled:                true,
		ReplicationDestination:            "dest-bucket",
		ReplicationFilter:                 "prefix/",
		ProvisionState:                    "pending",
		ResourceVersion:                   7,
		CreatedAt:                         pgTS(created),
		UpdatedAt:                         pgTS(updated),
	}
	list := sqlc.ListBucketsV2Row{
		BackendName:                       get.BackendName,
		Name:                              get.Name,
		DisplayName:                       get.DisplayName,
		Region:                            get.Region,
		Labels:                            get.Labels,
		OwnerTenantID:                     get.OwnerTenantID,
		CedarPolicy:                       get.CedarPolicy,
		Constraints:                       get.Constraints,
		LifecycleRules:                    get.LifecycleRules,
		ObjectLockEnabled:                 get.ObjectLockEnabled,
		ObjectLockDefaultMode:             get.ObjectLockDefaultMode,
		ObjectLockDefaultRetentionSeconds: get.ObjectLockDefaultRetentionSeconds,
		VersioningEnabled:                 get.VersioningEnabled,
		VersioningKeepDeletesForever:      get.VersioningKeepDeletesForever,
		ReplicationEnabled:                get.ReplicationEnabled,
		ReplicationDestination:            get.ReplicationDestination,
		ReplicationFilter:                 get.ReplicationFilter,
		ProvisionState:                    get.ProvisionState,
		ResourceVersion:                   get.ResourceVersion,
		CreatedAt:                         get.CreatedAt,
		UpdatedAt:                         get.UpdatedAt,
	}
	accessible := sqlc.ListAccessibleBucketsRow{
		BackendName:                       get.BackendName,
		Name:                              get.Name,
		DisplayName:                       get.DisplayName,
		Region:                            get.Region,
		Labels:                            get.Labels,
		OwnerTenantID:                     get.OwnerTenantID,
		CedarPolicy:                       get.CedarPolicy,
		Constraints:                       get.Constraints,
		LifecycleRules:                    get.LifecycleRules,
		ObjectLockEnabled:                 get.ObjectLockEnabled,
		ObjectLockDefaultMode:             get.ObjectLockDefaultMode,
		ObjectLockDefaultRetentionSeconds: get.ObjectLockDefaultRetentionSeconds,
		VersioningEnabled:                 get.VersioningEnabled,
		VersioningKeepDeletesForever:      get.VersioningKeepDeletesForever,
		ReplicationEnabled:                get.ReplicationEnabled,
		ReplicationDestination:            get.ReplicationDestination,
		ReplicationFilter:                 get.ReplicationFilter,
		ProvisionState:                    get.ProvisionState,
		ResourceVersion:                   get.ResourceVersion,
		CreatedAt:                         get.CreatedAt,
		UpdatedAt:                         get.UpdatedAt,
	}

	t.Run("get", func(t *testing.T) { wantBucket(t, bucketFromV2Row(get), owner, created, updated) })
	t.Run("list", func(t *testing.T) { wantBucket(t, bucketFromV2RowList(list), owner, created, updated) })
	t.Run("accessible", func(t *testing.T) {
		wantBucket(t, bucketFromV2RowAccessible(accessible), owner, created, updated)
	})
}

// An absent object-lock mode is NULL in the column and "" in the domain, and
// a bucket with no owner is a NULL uuid rather than a zero one that a tenant
// lookup would go looking for.
func TestBucketRowMapperNulls(t *testing.T) {
	got := bucketFromV2Row(sqlc.GetBucketV2Row{
		BackendName: "b",
		Name:        "n",
	})
	if got.ObjectLock.DefaultMode != "" {
		t.Errorf("DefaultMode = %q, want \"\"", got.ObjectLock.DefaultMode)
	}
	if got.OwnerTenantID != uuid.Nil {
		t.Errorf("OwnerTenantID = %v, want Nil", got.OwnerTenantID)
	}
	if !got.CreatedAt.IsZero() || !got.UpdatedAt.IsZero() {
		t.Errorf("timestamps = (%v, %v), want zero", got.CreatedAt, got.UpdatedAt)
	}
	if got.Labels != nil {
		t.Errorf("Labels = %v, want nil", got.Labels)
	}
}
