-- Tenant default-binding queries.
--
-- One row per tenant. Set at CreateTenant time; updated by future
-- SetTenantDefaultBinding RPC; deleted CASCADE when the tenant is
-- deleted; deletion of the underlying bucket is RESTRICTed so an
-- operator must rebind before tearing down the bucket.

-- name: SetTenantDefaultBinding :exec
INSERT INTO tenant_default_bindings (tenant_id, backend_id, bucket_name, set_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id) DO UPDATE
   SET backend_id  = EXCLUDED.backend_id,
       bucket_name = EXCLUDED.bucket_name,
       set_at      = now(),
       set_by      = EXCLUDED.set_by;

-- name: GetTenantDefaultBinding :one
SELECT tenant_id, backend_id, bucket_name, set_at, set_by
FROM tenant_default_bindings
WHERE tenant_id = $1;

-- name: ClearTenantDefaultBinding :execrows
DELETE FROM tenant_default_bindings
WHERE tenant_id = $1;
