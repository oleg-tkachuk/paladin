-- Tenant default-binding queries.
--
-- One row per tenant. Set at CreateTenant time; updated by future
-- SetTenantDefaultBinding RPC; deleted CASCADE when the tenant is
-- deleted; deletion of the underlying bucket is RESTRICTed so an
-- operator must rebind before tearing down the bucket.

-- name: SetTenantDefaultBinding :exec
INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
SELECT $1, b.id, $4
FROM buckets b
JOIN storage_backends sb ON sb.id = b.backend_id
WHERE sb.name = $2 AND b.name = $3
ON CONFLICT (tenant_id) DO UPDATE
   SET bucket_id  = EXCLUDED.bucket_id = EXCLUDED.bucket_id,
       set_at      = now(),
       set_by      = EXCLUDED.set_by;

-- name: GetTenantDefaultBinding :one
SELECT tenant_id, bucket_id, set_at, set_by
FROM tenant_default_bindings
WHERE tenant_id = $1;

-- name: ClearTenantDefaultBinding :execrows
DELETE FROM tenant_default_bindings
WHERE tenant_id = $1;
