-- +goose Up

-- Indexed object search under row-level security.
--
-- WHY THIS EXISTS. Under RLS Postgres may evaluate a query's own predicates
-- only after the policy's, unless the operator is LEAKPROOF — and `@>` (jsonb)
-- and LIKE are not. So for the runtime role the indexes 026–028 are unusable:
-- `tags @> '{"env":"prod"}'` and `path LIKE '%q%'` become filters on a scan of
-- the whole tenant, ~50x slower at 200k objects and growing linearly. Marking
-- the operators LEAKPROOF needs a superuser and is not true of textlike.
--
-- THE SHAPE. This function finds candidate ids with the indexes, running as
-- its owner — the migrate role, BYPASSRLS, so the policy is not in the plan.
-- It returns ids only, and the caller (the ListObjects query) reads the rows
-- through a plain `id = ANY(...)` under the caller's own RLS. So the policy
-- still decides every row a client sees; this function only decides which
-- rows get looked at. A mistake in it can make a search slower or emptier,
-- never show another tenant's object.
--
-- It still refuses to search a tenant the session is not scoped to, with the
-- very functions the policy uses (002): otherwise the ids it returns — and
-- the time it takes — would answer questions about another tenant's keys.
--
-- Dynamic SQL, because a catch-all `(p IS NULL OR col @> p)` query gets one
-- generic plan that can use none of the indexes. Only fixed fragments are
-- concatenated; every caller value travels through USING.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION search_object_ids(
    p_tenant_id           uuid,
    p_collection          text,
    p_state               object_state,
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
    q text := 'SELECT o.id FROM public.objects o'
           || ' JOIN public.collections c ON c.id = o.collection_id'
           || ' WHERE o.tenant_id = $1 AND c.tenant_id = $1 AND c.name = $2';
BEGIN
    IF p_tenant_id IS NULL
       OR NOT (p_tenant_id IS NOT DISTINCT FROM public.paladin_session_tenant_id()
               OR public.paladin_session_cross_tenant()) THEN
        RETURN;
    END IF;
    IF p_limit IS NULL OR p_limit < 1 THEN
        RETURN;
    END IF;
    -- LIKE patterns arrive escaped by the caller (backslash, the default).
    IF p_state IS NOT NULL THEN
        q := q || ' AND o.state = $3';
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
              p_after_id, p_limit;
END
$$;
-- +goose StatementEnd

-- A SECURITY DEFINER function is executable by PUBLIC unless revoked.
REVOKE ALL ON FUNCTION search_object_ids(
    uuid, text, object_state, text, text, text, text, jsonb, jsonb, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION search_object_ids(
    uuid, text, object_state, text, text, text, text, jsonb, jsonb, uuid, integer) TO paladin_app;

-- +goose Down
DROP FUNCTION IF EXISTS search_object_ids(
    uuid, text, object_state, text, text, text, text, jsonb, jsonb, uuid, integer);
