-- +goose Up

-- An audit listing narrows by three facets — the plane the call came in on
-- (actor_audience), its outcome (an error message or none) and the service
-- it called (audit_service) — and counts each (ListAuditLog's facet_counts).
-- audit_service names the service once, for the listing and the counts.

-- +goose StatementBegin
CREATE FUNCTION audit_service(p_action text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    -- A call's action is its procedure, /pkg.Service/Method; the service is
    -- pkg.Service. An action of the server's own, iam.bootstrap_admin, is
    -- its own service.
    SELECT CASE WHEN p_action LIKE '/%/%' THEN split_part(p_action, '/', 2) ELSE p_action END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION audit_service(text) TO paladin_app;

DROP FUNCTION list_audit_entry_ids(text, uuid, uuid, text, text, timestamptz, timestamptz, text, boolean, text, uuid, integer);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION list_audit_entry_ids(
    p_actor_subject   text,
    p_actor_tenant_id uuid,
    p_trail_tenant_id uuid,
    p_action_eq       text,
    p_action_prefix   text,
    p_at_gte          timestamptz,
    p_at_lte          timestamptz,
    p_audiences       text[],
    p_is_error        boolean,
    p_services        text[],
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
            v_after := '$11::timestamptz';
        WHEN 'action' THEN
            v_key := 'a.action COLLATE "C"';
            v_after := '$11 COLLATE "C"';
        WHEN 'actor_subject' THEN
            v_key := 'a.actor_subject COLLATE "C"';
            v_after := '$11 COLLATE "C"';
        WHEN 'resource_name' THEN
            v_key := 'a.resource_name COLLATE "C"';
            v_after := '$11 COLLATE "C"';
        -- NULL, a call that sent no User-Agent, sorts as empty: the keyset
        -- compares (value, id), and a NULL value would compare as neither.
        WHEN 'user_agent' THEN
            v_key := 'COALESCE(a.user_agent, '''') COLLATE "C"';
            v_after := '$11 COLLATE "C"';
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
    -- The facets: each a set of answers an entry gives one of.
    IF p_audiences IS NOT NULL THEN
        q := q || ' AND a.actor_audience = ANY($8)';
    END IF;
    IF p_is_error IS NOT NULL THEN
        q := q || ' AND (COALESCE(a.error_message, '''') <> '''') = $9';
    END IF;
    IF p_services IS NOT NULL THEN
        q := q || ' AND public.audit_service(a.action) = ANY($10)';
    END IF;
    IF p_after_id IS NOT NULL THEN
        q := q || ' AND (' || v_key || ', a.id) '
               || CASE WHEN p_desc THEN '<' ELSE '>' END
               || ' (' || v_after || ', $12)';
    END IF;
    q := q || ' ORDER BY ' || v_key || CASE WHEN p_desc THEN ' DESC' ELSE '' END
           || ', a.id' || CASE WHEN p_desc THEN ' DESC' ELSE '' END
           || ' LIMIT $13';
    RETURN QUERY EXECUTE q
        USING p_actor_subject, p_actor_tenant_id, p_trail_tenant_id,
              p_action_eq, p_action_prefix, p_at_gte, p_at_lte,
              p_audiences, p_is_error, p_services,
              p_after_value, p_after_id, p_limit;
END
$$;
-- +goose StatementEnd


REVOKE ALL ON FUNCTION list_audit_entry_ids(
    text, uuid, uuid, text, text, timestamptz, timestamptz, text[], boolean, text[], text, boolean, text, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION list_audit_entry_ids(
    text, uuid, uuid, text, text, timestamptz, timestamptz, text[], boolean, text[], text, boolean, text, uuid, integer) TO paladin_app;

-- +goose Down

DROP FUNCTION list_audit_entry_ids(text, uuid, uuid, text, text, timestamptz, timestamptz, text[], boolean, text[], text, boolean, text, uuid, integer);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION list_audit_entry_ids(
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
        -- NULL, a call that sent no User-Agent, sorts as empty: the keyset
        -- compares (value, id), and a NULL value would compare as neither.
        WHEN 'user_agent' THEN
            v_key := 'COALESCE(a.user_agent, '''') COLLATE "C"';
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

DROP FUNCTION audit_service(text);
