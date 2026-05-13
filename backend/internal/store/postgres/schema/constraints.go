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

// tenants table constraint names — see migrations 001 (PK + initial
// columns), 009 (slug UNIQUE + format), 033 (display_name UNIQUE +
// format + immutability trigger).
const (
	// TenantsPK — primary key on tenants.tenant_id (UUID).
	TenantsPK = "tenants_pkey"
	// TenantsSlugUnique — UNIQUE (slug). Phase 0 enforced.
	TenantsSlugUnique = "tenants_slug_unique"
	// TenantsSlugFormat — CHECK that slug matches kebab-case.
	TenantsSlugFormat = "tenants_slug_format"
	// TenantsDisplayNameUnique — UNIQUE (display_name). Migration 033.
	TenantsDisplayNameUnique = "tenants_display_name_unique"
	// TenantsDisplayNameFormat — CHECK on display_name length + trim.
	TenantsDisplayNameFormat = "tenants_display_name_format"
	// TenantsImmutableColumnsTrigger — BEFORE UPDATE trigger that
	// blocks slug + tenant_id mutation outside the rename-RPC path.
	TenantsImmutableColumnsTrigger = "tenants_immutable_columns"
)

// object_keys table constraint names — see migration 005 (FK to
// buckets), 030 (multi-segment path CHECK).
const (
	// ObjectKeysPK — composite primary key (tenant_id, object_key).
	ObjectKeysPK = "object_keys_pkey"
	// ObjectKeysTenantIDFK — FK to tenants.tenant_id.
	ObjectKeysTenantIDFK = "object_keys_tenant_id_fkey"
	// ObjectKeysBucketFK — composite FK to (buckets.backend_id,
	// buckets.bucket_name).
	ObjectKeysBucketFK = "object_keys_backend_id_fkey"
	// ObjectKeysFormat — CHECK accepting multi-segment slash-separated
	// kebab-case paths (post-migration 030).
	ObjectKeysFormat = "object_key_format"
)

// tenant_default_bindings constraint names — see migration 034.
const (
	// TenantDefaultBindingsPK — PRIMARY KEY (tenant_id).
	TenantDefaultBindingsPK = "tenant_default_bindings_pkey"
	// TenantDefaultBindingsTenantFK — FK to tenants(tenant_id)
	// ON DELETE CASCADE.
	TenantDefaultBindingsTenantFK = "tenant_default_bindings_tenant_id_fkey"
	// TenantDefaultBindingsBucketFK — composite FK to
	// buckets(backend_id, bucket_name) ON DELETE RESTRICT.
	TenantDefaultBindingsBucketFK = "tenant_default_bindings_backend_id_fkey"
)

// buckets table constraint names — see migration 003.
const (
	// BucketsPK — composite (backend_id, bucket_name).
	BucketsPK = "buckets_pkey"
	// BucketsBackendFK — FK to backends.backend_id.
	BucketsBackendFK = "buckets_backend_id_fkey"
)
