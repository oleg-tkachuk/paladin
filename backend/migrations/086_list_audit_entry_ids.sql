-- +goose Up

-- An audit listing sorts by the field the caller names — at, action,
-- actor_subject or resource_name — and pages through that order with a keyset
-- on (field, id). The list used to be newest first only.
--
-- The ids of one page, in order; the caller reads those rows. SECURITY
-- INVOKER, so audit_log's row-level security still decides what a caller
-- sees. The predicates are ListAuditEntries' own, which this replaces.
--
-- No index backs the text orders: at 39k rows a page sorted by resource_name
-- read 6 ms against 0.16 ms for the indexed newest-first page (2026-10-18).
-- An index per order is the step to take when the log outgrows that.

-- +goose StatementBegin
CREATE FUNCTION list_audit_entry_ids(
    p_actor_subject   text,
    p_actor_tenant_id uuid,
    p_trail_tenant_id uuid,
    p_action_eq       text,
    p_action_prefix   text,
    p_at_gte          timestamptz,
    p_at_lte          timestamptz,
    p_order           text,
    p_desc            boolean,
    p_after_value     text,
    p_after_id        uuid,
    p_limit           integer
) RETURNS SETOF uuid
    LANGUAGE plpgsql STABLE SECURITY INVOKER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    -- The sort key over a, and the cursor value cast to compare with it. Text
    -- sorts bytewise (COLLATE "C"), as the caller's comparison does.
    v_key   text;
    v_after text;
    q text := 'SELECT a.id FROM public.audit_log a WHERE true';
BEGIN
    -- One of a fixed set, named by the caller's allowlist; its name never
    -- reaches the query text.
    CASE p_order
        WHEN 'at' THEN
            v_key := 'a.at';
            v_after := '$8::timestamptz';
        WHEN 'action' THEN
            v_key := 'a.action COLLATE "C"';
            v_after := '$8 COLLATE "C"';
        WHEN 'actor_subject' THEN
            v_key := 'a.actor_subject COLLATE "C"';
            v_after := '$8 COLLATE "C"';
        WHEN 'resource_name' THEN
            v_key := 'a.resource_name COLLATE "C"';
            v_after := '$8 COLLATE "C"';
        ELSE
            RAISE EXCEPTION 'list_audit_entry_ids: unknown order %', p_order
                USING ERRCODE = 'invalid_parameter_value';
    END CASE;
    IF p_limit IS NULL OR p_limit < 1 THEN
        RETURN;
    END IF;
    IF p_actor_subject IS NOT NULL THEN
        q := q || ' AND a.actor_subject = $1';
    END IF;
    IF p_actor_tenant_id IS NOT NULL THEN
        q := q || ' AND a.actor_tenant_id = $2';
    END IF;
    -- A tenant's trail: what its principals did, and what was done to it.
    IF p_trail_tenant_id IS NOT NULL THEN
        q := q || ' AND (a.actor_tenant_id = $3 OR a.resource_tenant_id = $3)';
    END IF;
    IF p_action_eq IS NOT NULL THEN
        q := q || ' AND a.action = $4';
    END IF;
    -- Arrives LIKE-escaped by the caller (backslash, the default).
    IF p_action_prefix IS NOT NULL THEN
        q := q || ' AND a.action LIKE $5';
    END IF;
    IF p_at_gte IS NOT NULL THEN
        q := q || ' AND a.at >= $6';
    END IF;
    IF p_at_lte IS NOT NULL THEN
        q := q || ' AND a.at <= $7';
    END IF;
    IF p_after_id IS NOT NULL THEN
        q := q || ' AND (' || v_key || ', a.id) '
               || CASE WHEN p_desc THEN '<' ELSE '>' END
               || ' (' || v_after || ', $9)';
    END IF;
    q := q || ' ORDER BY ' || v_key || CASE WHEN p_desc THEN ' DESC' ELSE '' END
           || ', a.id' || CASE WHEN p_desc THEN ' DESC' ELSE '' END
           || ' LIMIT $10';
    RETURN QUERY EXECUTE q
        USING p_actor_subject, p_actor_tenant_id, p_trail_tenant_id,
              p_action_eq, p_action_prefix, p_at_gte, p_at_lte,
              p_after_value, p_after_id, p_limit;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION list_audit_entry_ids(
    text, uuid, uuid, text, text, timestamptz, timestamptz, text, boolean, text, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION list_audit_entry_ids(
    text, uuid, uuid, text, text, timestamptz, timestamptz, text, boolean, text, uuid, integer) TO paladin_app;

-- +goose Down
DROP FUNCTION IF EXISTS list_audit_entry_ids(
    text, uuid, uuid, text, text, timestamptz, timestamptz, text, boolean, text, uuid, integer);
