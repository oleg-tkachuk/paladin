package admindomain

// The relations that hold a tenant or a bucket back from a delete, by the
// table name Postgres reports on the foreign-key violation and
// CountBucketReferences returns. They name a table, which a caller has never
// seen; RelationNoun is what the caller is told instead.
const (
	RelationUsers                  = "users"
	RelationUserSettings           = "user_settings"
	RelationBuckets                = "buckets"
	RelationCollections            = "collections"
	RelationCharges                = "charges"
	RelationChargeRefunds          = "charge_refunds"
	RelationTenantDefaultBindings  = "tenant_default_bindings"
	RelationTenantStorageMigration = "tenant_storage_migrations"
	RelationMultipartUploads       = "multipart_uploads"
	RelationPendingPurges          = "pending_purges"
	RelationPendingMultipartAborts = "pending_multipart_aborts"
	// RelationObjects holds a bucket while an object's bytes are placed in
	// it (objects.placement_bucket_id, ADR-0051).
	RelationObjects = "objects"
)

// relationOther is what a caller is told for a relation with no noun: the
// table's name goes to the log, never into the message.
const relationOther = "other records"

// relationNouns are the resources a caller knows each relation as.
var relationNouns = map[string]string{
	RelationUsers:                  "users",
	RelationUserSettings:           "user settings",
	RelationBuckets:                "owned buckets",
	RelationCollections:            "collections",
	RelationCharges:                "billing charges",
	RelationChargeRefunds:          "billing refunds",
	RelationTenantDefaultBindings:  "tenant default bindings",
	RelationTenantStorageMigration: "storage migrations",
	RelationMultipartUploads:       "multipart uploads",
	RelationPendingPurges:          "objects awaiting purge",
	RelationPendingMultipartAborts: "multipart uploads awaiting abort",
	RelationObjects:                "objects stored in it",
}

// RelationNoun is the resource a caller knows relation as, and "other
// records" for one it has no name for.
func RelationNoun(relation string) string {
	if n, ok := relationNouns[relation]; ok {
		return n
	}
	return relationOther
}

// HasRelationNoun reports whether relation has a name a caller knows.
func HasRelationNoun(relation string) bool {
	_, ok := relationNouns[relation]
	return ok
}
