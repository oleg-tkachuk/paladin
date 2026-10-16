-- +goose Up

-- An object list whose filter names no state lists the live objects.
--
-- With no state filter search_object_ids returned trashed objects beside live
-- ones, so a collection's objects showed what its trash shows too. Whether a
-- filter names state is a question about the whole expression: a disjunction
-- or an `in` list pushes no single state down. So the caller answers it and
-- passes p_live_only. A new signature, so the old function is dropped.
DROP FUNCTION IF EXISTS search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer);

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

-- +goose Down
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
    p_limit               integer
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
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION search_object_ids(
    uuid, text, text, text, text, text, text, jsonb, jsonb, uuid, integer) TO paladin_app;
