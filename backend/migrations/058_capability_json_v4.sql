-- +goose Up

-- limes v0.4.0 names the JSON of its caveats and principals in snake_case and
-- writes an amount as a string; a row in the old spelling no longer decodes.
-- Rewrites the rows that still carry it, and fills 057's ceilings from them.
-- Rows already in the new spelling keep it; the ceilings are read from
-- whichever spelling the row has.

-- A helper for this migration only; dropped below.
CREATE FUNCTION limes_principal_v4(p jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN p IS NULL OR NOT (p ? 'Subject') THEN p ELSE
        jsonb_strip_nulls(jsonb_build_object(
            'type',      p->'Type',
            'tenant_id', p->'TenantID',
            'subject',   p->'Subject',
            'agent', CASE WHEN jsonb_typeof(p->'Agent') = 'object' THEN jsonb_build_object(
                'agent_type',      p->'Agent'->'AgentType',
                'agent_version',   p->'Agent'->'AgentVersion',
                'run_id',          p->'Agent'->'RunID',
                'parent_agent_id', p->'Agent'->'ParentAgentID',
                'model',           p->'Agent'->'Model',
                'mcp_client',      p->'Agent'->'MCPClient') END))
    END
$$;

UPDATE capability_records
SET caveats = jsonb_strip_nulls(jsonb_build_object(
        'ops',                      caveats->'Ops',
        'resource_prefixes',        caveats->'ResourcePrefixes',
        'resource_uris',            caveats->'ResourceURIs',
        'max_requests',             caveats->'MaxRequests',
        'max_budget',               to_jsonb(caveats->>'MaxBudgetAmount'),
        'unit_code',                caveats->'UnitCode',
        'allow_tainted_read',       caveats->'AllowTaintedRead',
        'idempotency_key_required', caveats->'IdempotencyKeyRequired',
        'source_ip_cidr',           caveats->'SourceIPCIDR')),
    principal_payload = limes_principal_v4(principal_payload),
    issued_by         = limes_principal_v4(issued_by)
WHERE caveats ? 'Ops';

DROP FUNCTION limes_principal_v4(jsonb);

UPDATE capability_records
SET max_budget   = COALESCE((caveats->>'max_budget')::numeric, 0),
    max_requests = COALESCE((caveats->>'max_requests')::bigint, 0)
WHERE max_budget IS NULL OR max_requests IS NULL;

-- +goose Down
-- The old spelling is not restored: the code it belongs to cannot read the
-- tokens this release issues either.
SELECT 1;
