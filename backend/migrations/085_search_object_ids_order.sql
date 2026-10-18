-- +goose Up

-- An object list sorts by the field the caller names, and pages through that
-- order with a keyset on (field, id). Until now the list was always in id
-- order whatever order_by said, so the console's sortable object columns
-- changed nothing. A new signature, so the old function is dropped.
DROP FUNCTION IF EXISTS search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer, boolean);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION search_object_ids(
    p_tenant_id           uuid,
    p_collection          text,
    p_state               text,
    p_prefix              text,
    p_substr              text,
    p_content_type        text,
    p_content_type_prefix text,
    p_tags                jsonb,
    p_metadata            jsonb,
    p_after_id            uuid,
    p_limit               integer,
    p_live_only           boolean,
    p_order               text,
    p_desc                boolean,
    p_after_value         text
) RETURNS SETOF uuid
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    v_collection_id uuid;
    -- The sort key, as an expression over o, and the cursor value cast to
    -- compare with it. Text sorts in byte order (COLLATE "C") whatever the
    -- database's collation, so the order matches the caller's own merge of
    -- several branches. A nullable column is coalesced, so no row sorts
    -- outside the keyset.
    v_key   text;
    v_after text;
    q text := 'SELECT o.id FROM public.objects o'
           || ' WHERE o.tenant_id = $1 AND o.collection_id = $12';
BEGIN
    -- The order is one of a fixed set, named by the caller's allowlist; its
    -- name never reaches the query text. Checked before anything else, so a
    -- bad argument is refused whoever calls.
    CASE coalesce(p_order, 'id')
        WHEN 'id' THEN
            v_key := NULL;
        WHEN 'key' THEN
            v_key := 'o.path COLLATE "C"';
            v_after := '$13 COLLATE "C"';
        WHEN 'content_type' THEN
            v_key := 'o.content_type COLLATE "C"';
            v_after := '$13 COLLATE "C"';
        WHEN 'state' THEN
            v_key := '(o.state::text) COLLATE "C"';
            v_after := '$13 COLLATE "C"';
        WHEN 'size_bytes' THEN
            v_key := 'coalesce(o.size_bytes, 0)';
            v_after := '$13::bigint';
        WHEN 'created_at' THEN
            v_key := 'o.created_at';
            v_after := '$13::timestamptz';
        WHEN 'terminated_at' THEN
            v_key := 'coalesce(o.terminated_at, ''epoch''::timestamptz)';
            v_after := '$13::timestamptz';
        ELSE
            RAISE EXCEPTION 'search_object_ids: unknown order %', p_order
                USING ERRCODE = 'invalid_parameter_value';
    END CASE;
    IF p_tenant_id IS NULL
       OR NOT (p_tenant_id IS NOT DISTINCT FROM public.paladin_session_tenant_id()
               OR public.paladin_session_cross_tenant()) THEN
        RETURN;
    END IF;
    IF p_limit IS NULL OR p_limit < 1 THEN
        RETURN;
    END IF;
    -- Resolved first rather than joined, so the query below names one known
    -- collection id and can walk idx_objects_keyset_state (030) in id order.
    -- Joined by name, the planner could not size the collection and sorted
    -- all of it for every page: 13 ms against 4 ms for a page of 500 out of
    -- 5000, growing with the collection.
    SELECT c.id INTO v_collection_id
      FROM public.collections c
     WHERE c.tenant_id = p_tenant_id AND c.name = p_collection;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    -- A state the enum does not hold matches no object. Cast as text, it
    -- would fail the whole query instead; compared as text, the column would
    -- leave the index. So: answer it here, and cast only a known value.
    IF p_state IS NOT NULL THEN
        IF NOT p_state = ANY (enum_range(NULL::public.object_state)::text[]) THEN
            RETURN;
        END IF;
        q := q || ' AND o.state = $3::public.object_state';
    END IF;
    -- LIKE patterns arrive escaped by the caller (backslash, the default).
    -- The live objects only: a filter that does not name state lists no
    -- trashed one. The caller decides, since it alone reads the whole filter.
    IF p_live_only THEN
        q := q || ' AND o.state <> ''DELETED''::public.object_state';
    END IF;
    IF p_prefix IS NOT NULL THEN
        q := q || ' AND o.path LIKE $4 || ''%''';
    END IF;
    IF p_substr IS NOT NULL THEN
        q := q || ' AND o.path LIKE ''%'' || $5 || ''%''';
    END IF;
    IF p_content_type IS NOT NULL THEN
        q := q || ' AND o.content_type = $6';
    END IF;
    IF p_content_type_prefix IS NOT NULL THEN
        q := q || ' AND o.content_type LIKE $7 || ''%''';
    END IF;
    IF p_tags IS NOT NULL THEN
        q := q || ' AND o.tags @> $8';
    END IF;
    IF p_metadata IS NOT NULL THEN
        q := q || ' AND o.metadata @> $9';
    END IF;
    IF v_key IS NULL THEN
        IF p_after_id IS NOT NULL THEN
            q := q || CASE WHEN p_desc THEN ' AND o.id < $10' ELSE ' AND o.id > $10' END;
        END IF;
        q := q || ' ORDER BY o.id' || CASE WHEN p_desc THEN ' DESC' ELSE '' END;
    ELSE
        IF p_after_id IS NOT NULL THEN
            q := q || ' AND (' || v_key || ', o.id) '
                   || CASE WHEN p_desc THEN '<' ELSE '>' END
                   || ' (' || v_after || ', $10)';
        END IF;
        q := q || ' ORDER BY ' || v_key || CASE WHEN p_desc THEN ' DESC' ELSE '' END
               || ', o.id' || CASE WHEN p_desc THEN ' DESC' ELSE '' END;
    END IF;
    q := q || ' LIMIT $11';
    RETURN QUERY EXECUTE q
        USING p_tenant_id, p_collection, p_state, p_prefix, p_substr,
              p_content_type, p_content_type_prefix, p_tags, p_metadata,
              p_after_id, p_limit, v_collection_id, p_after_value;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer, boolean, text, boolean, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer, boolean, text, boolean, text) TO paladin_app;

-- +goose Down
DROP FUNCTION IF EXISTS search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer, boolean, text, boolean, text);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION search_object_ids(
    p_tenant_id           uuid,
    p_collection          text,
    p_state               text,
    p_prefix              text,
    p_substr              text,
    p_content_type        text,
    p_content_type_prefix text,
    p_tags                jsonb,
    p_metadata            jsonb,
    p_after_id            uuid,
    p_limit               integer,
    p_live_only           boolean
) RETURNS SETOF uuid
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    v_collection_id uuid;
    q text := 'SELECT o.id FROM public.objects o'
           || ' WHERE o.tenant_id = $1 AND o.collection_id = $12';
BEGIN
    IF p_tenant_id IS NULL
       OR NOT (p_tenant_id IS NOT DISTINCT FROM public.paladin_session_tenant_id()
               OR public.paladin_session_cross_tenant()) THEN
        RETURN;
    END IF;
    IF p_limit IS NULL OR p_limit < 1 THEN
        RETURN;
    END IF;
    -- Resolved first rather than joined, so the query below names one known
    -- collection id and can walk idx_objects_keyset_state (030) in id order.
    -- Joined by name, the planner could not size the collection and sorted
    -- all of it for every page: 13 ms against 4 ms for a page of 500 out of
    -- 5000, growing with the collection.
    SELECT c.id INTO v_collection_id
      FROM public.collections c
     WHERE c.tenant_id = p_tenant_id AND c.name = p_collection;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    -- A state the enum does not hold matches no object. Cast as text, it
    -- would fail the whole query instead; compared as text, the column would
    -- leave the index. So: answer it here, and cast only a known value.
    IF p_state IS NOT NULL THEN
        IF NOT p_state = ANY (enum_range(NULL::public.object_state)::text[]) THEN
            RETURN;
        END IF;
        q := q || ' AND o.state = $3::public.object_state';
    END IF;
    -- LIKE patterns arrive escaped by the caller (backslash, the default).
    -- The live objects only: a filter that does not name state lists no
    -- trashed one. The caller decides, since it alone reads the whole filter.
    IF p_live_only THEN
        q := q || ' AND o.state <> ''DELETED''::public.object_state';
    END IF;
    IF p_prefix IS NOT NULL THEN
        q := q || ' AND o.path LIKE $4 || ''%''';
    END IF;
    IF p_substr IS NOT NULL THEN
        q := q || ' AND o.path LIKE ''%'' || $5 || ''%''';
    END IF;
    IF p_content_type IS NOT NULL THEN
        q := q || ' AND o.content_type = $6';
    END IF;
    IF p_content_type_prefix IS NOT NULL THEN
        q := q || ' AND o.content_type LIKE $7 || ''%''';
    END IF;
    IF p_tags IS NOT NULL THEN
        q := q || ' AND o.tags @> $8';
    END IF;
    IF p_metadata IS NOT NULL THEN
        q := q || ' AND o.metadata @> $9';
    END IF;
    IF p_after_id IS NOT NULL THEN
        q := q || ' AND o.id > $10';
    END IF;
    q := q || ' ORDER BY o.id LIMIT $11';
    RETURN QUERY EXECUTE q
        USING p_tenant_id, p_collection, p_state, p_prefix, p_substr,
              p_content_type, p_content_type_prefix, p_tags, p_metadata,
              p_after_id, p_limit, v_collection_id;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer, boolean) TO paladin_app;
