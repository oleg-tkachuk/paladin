// Package schema centralises the names of DB-level constraints,
// indexes, and triggers that application code matches on (e.g. when
// translating pg_error.ConstraintName to a typed sentinel). Keeping
// these as constants — instead of literal strings sprinkled across
// adapters — means a migration that renames a constraint forces a
// compile error in every site that depended on the old name,
// instead of silently degrading to "operation failed: <opaque pg
// error>" in production.
//
// Convention: name the Go constant after the SQL constraint with
// the same identifier (snake_case), so a grep across schema +
// adapters shows the full chain.
package schema

// tenants table constraint names — see 001_initial_schema.sql for the
// columns and CHECKs, 003_triggers.sql for the immutability trigger.
const (
	// TenantsPK — primary key on tenants.tenant_id (UUID).
	TenantsPK = "tenants_pkey"
	// TenantsSlugUnique — the partial unique index over live tenants.
	// Soft-deleted rows keep their slug, so uniqueness is scoped to
	// deleted_at IS NULL and the index carries that name, not a
	// constraint name.
	TenantsSlugUnique = "tenants_slug_live_key"
	// TenantsSlugFormat — CHECK that slug matches kebab-case.
	TenantsSlugFormat = "tenants_slug_format"
	// TenantsDisplayNameUnique — partial unique index, same scoping as
	// the slug one.
	TenantsDisplayNameUnique = "tenants_display_name_live_key"
	// TenantsDisplayNameFormat — CHECK on display_name length + trim.
	TenantsDisplayNameFormat = "tenants_display_name_format"
	// TenantsImmutableColumnsTrigger — BEFORE UPDATE trigger that
	// blocks slug + tenant_id mutation outside the rename-RPC path.
	TenantsImmutableColumnsTrigger = "tenants_block_immutable_columns"
)

// collections table constraint names — see the schema baseline
// (001_initial_schema.sql): FK to buckets, multi-segment path CHECK.
const (
	// CollectionsPK — composite primary key (tenant_id, collection).
	CollectionsPK = "collections_pkey"
	// CollectionsTenantIDFK — FK to tenants.tenant_id.
	CollectionsTenantIDFK = "collections_tenant_id_fkey"
	// CollectionsBucketFK — composite FK to (buckets.backend_id,
	// buckets.bucket_name).
	CollectionsBucketFK = "collections_bucket_id_fkey"
	// CollectionsFormat — CHECK accepting multi-segment slash-separated
	// kebab-case paths (post-the schema baseline (001_initial_schema.sql)).
	CollectionsFormat = "collections_name_format"
	// CollectionsNameUnique — UNIQUE (tenant_id, collection). This is the
	// one a duplicate CreateCollection trips, and until it was matched the
	// caller got its raw text as CodeInternal.
	CollectionsNameUnique = "collections_tenant_id_name_key"

	// CollectionsBucketIDColumn — collections.bucket_id, NOT NULL. The create
	// query resolves it from the backend and bucket names by subquery, so an
	// unknown bucket arrives as a NOT NULL violation on this column.
	CollectionsBucketIDColumn = "bucket_id"

	// The rules of public collections (049_public_collections.sql, ADR-0027).
	// Raised by triggers, which name the rule as the error's constraint.
	//
	// CollectionsBucketVisibility — a collection's public_read equals its
	// bucket's.
	CollectionsBucketVisibility = "collections_bucket_visibility"
	// CollectionsPublicReadFixed — public_read and cache_control never change.
	CollectionsPublicReadFixed = "collections_public_read_fixed"
	// CollectionsPublicBucketFixed — a public collection never rebinds.
	CollectionsPublicBucketFixed = "collections_public_bucket_fixed"
	// BucketsPublicReadFixed — a bucket's public_read and public_base_url
	// never change.
	BucketsPublicReadFixed = "buckets_public_read_fixed"
)

// object_tags table constraint names — see the schema baseline
// (001_initial_schema.sql).
const (
	// ObjectTagsSlugUnique — UNIQUE (tenant_id, slug); what a duplicate
	// CreateObjectTag trips.
	ObjectTagsSlugUnique = "object_tags_tenant_id_slug_key"
)

// tenant_default_bindings constraint names — see the schema baseline (001_initial_schema.sql).
const (
	// TenantDefaultBindingsPK — PRIMARY KEY (tenant_id).
	TenantDefaultBindingsPK = "tenant_default_bindings_pkey"
	// TenantDefaultBindingsTenantFK — FK to tenants(tenant_id)
	// ON DELETE CASCADE.
	TenantDefaultBindingsTenantFK = "tenant_default_bindings_tenant_id_fkey"
	// TenantDefaultBindingsBucketFK — composite FK to
	// buckets(backend_id, bucket_name) ON DELETE RESTRICT. Postgres auto-names
	// a composite FK after ALL its columns, so it's `…_backend_id_bucket_name_fkey`
	// — not `…_backend_id_fkey`. The shorter name never matched, so the FK
	// violation was surfacing raw instead of as ErrDefaultBindingBucketMissing.
	TenantDefaultBindingsBucketFK = "tenant_default_bindings_bucket_id_fkey"
)

// buckets table constraint names — see `001_initial_schema.sql`.
const (
	// BucketsPK — composite (backend_id, bucket_name).
	BucketsPK = "buckets_pkey"
	// BucketsBackendFK — FK to backends.backend_id.
	BucketsBackendFK = "buckets_backend_id_fkey"
)
