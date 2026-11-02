-- +goose Up

-- A folder listing that walks idx_objects_collection_path_c (105): one index
-- probe per folder, rather than a read of every key under the prefix folded
-- with a DISTINCT. Measured on 10M objects in one collection, 500 folders at
-- its root: the root listing took 2.2 s, growing with the collection.

-- +goose StatementBegin
-- The least key greater than every key that begins with p, or NULL when no
-- key is. Keys compare bytewise (COLLATE "C") and UTF-8 keeps code point
-- order in its bytes, so that is p with its last code point raised by one;
-- a last code point that cannot be raised is dropped and the one before it
-- raised instead.
CREATE OR REPLACE FUNCTION paladin_key_successor(p text) RETURNS text
    LANGUAGE plpgsql IMMUTABLE STRICT
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    c_max_code_point     CONSTANT integer := 1114111; -- U+10FFFF
    c_before_surrogates  CONSTANT integer := 55295;   -- U+D7FF
    c_after_surrogates   CONSTANT integer := 57344;   -- U+E000, chr() refuses the surrogates between
    v_key  text := p;
    v_last integer;
BEGIN
    WHILE v_key <> '' LOOP
        v_last := ascii(right(v_key, 1));
        IF v_last < c_max_code_point THEN
            RETURN left(v_key, -1)
                || chr(CASE WHEN v_last = c_before_surrogates THEN c_after_surrogates ELSE v_last + 1 END);
        END IF;
        v_key := left(v_key, -1);
    END LOOP;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION list_object_prefixes(
    p_tenant_id  uuid,
    p_collection text,
    p_prefix     text,
    p_delimiter  text,
    p_state      text,
    p_live_only  boolean,
    p_limit      integer
) RETURNS SETOF text
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    v_collection_id uuid;
    v_prefix text := coalesce(p_prefix, '');
    -- The walk's window: from v_from, up to (not including) v_end, the first
    -- key past the prefix. No end when nothing follows the prefix.
    v_from  text;
    v_end   text;
    v_path  text;
    v_folder text;
    v_found integer := 0;
BEGIN
    -- The same gate as search_object_ids: this tenant's session, or one
    -- allowed across tenants.
    IF p_tenant_id IS NULL
       OR NOT (p_tenant_id IS NOT DISTINCT FROM public.paladin_session_tenant_id()
               OR public.paladin_session_cross_tenant()) THEN
        RETURN;
    END IF;
    IF p_delimiter IS NULL OR p_delimiter = '' OR p_limit IS NULL OR p_limit < 1 THEN
        RETURN;
    END IF;
    IF p_state IS NOT NULL
       AND NOT p_state = ANY (enum_range(NULL::public.object_state)::text[]) THEN
        RETURN;
    END IF;
    SELECT c.id INTO v_collection_id
      FROM public.collections c
     WHERE c.tenant_id = p_tenant_id AND c.name = p_collection;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    v_from := v_prefix;
    v_end := public.paladin_key_successor(v_prefix);
    -- Each turn finds the first key at or past v_from that lies below a
    -- folder, names its folder, and starts the next turn past every key in
    -- that folder. The folders come out in byte order, each once. Two
    -- queries rather than one with "v_end IS NULL OR", so the end stays an
    -- index condition and a walk that is done does not read on to the end of
    -- the collection.
    WHILE v_found < p_limit LOOP
        IF v_end IS NULL THEN
            SELECT o.path INTO v_path
              FROM public.objects o
             WHERE o.collection_id = v_collection_id
               AND o.path COLLATE "C" >= v_from
               AND o.tenant_id = p_tenant_id
               AND strpos(substr(o.path, char_length(v_prefix) + 1), p_delimiter) > 0
               AND (p_state IS NULL OR o.state = p_state::public.object_state)
               AND (NOT p_live_only OR o.state <> 'DELETED'::public.object_state)
             ORDER BY o.path COLLATE "C"
             LIMIT 1;
        ELSE
            SELECT o.path INTO v_path
              FROM public.objects o
             WHERE o.collection_id = v_collection_id
               AND o.path COLLATE "C" >= v_from
               AND o.path COLLATE "C" < v_end
               AND o.tenant_id = p_tenant_id
               AND strpos(substr(o.path, char_length(v_prefix) + 1), p_delimiter) > 0
               AND (p_state IS NULL OR o.state = p_state::public.object_state)
               AND (NOT p_live_only OR o.state <> 'DELETED'::public.object_state)
             ORDER BY o.path COLLATE "C"
             LIMIT 1;
        END IF;
        EXIT WHEN NOT FOUND;
        v_folder := v_prefix
                    || split_part(substr(v_path, char_length(v_prefix) + 1), p_delimiter, 1)
                    || p_delimiter;
        RETURN NEXT v_folder;
        v_found := v_found + 1;
        v_from := public.paladin_key_successor(v_folder);
        EXIT WHEN v_from IS NULL;
    END LOOP;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION paladin_key_successor(text) FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION list_object_prefixes(
    p_tenant_id  uuid,
    p_collection text,
    p_prefix     text,
    p_delimiter  text,
    p_state      text,
    p_live_only  boolean,
    p_limit      integer
) RETURNS SETOF text
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    v_collection_id uuid;
    v_prefix text := coalesce(p_prefix, '');
    -- The prefix as a LIKE pattern, for the trigram index; starts_with below
    -- is what decides.
    v_like text := replace(replace(replace(coalesce(p_prefix, ''),
                       '\', '\\'), '%', '\%'), '_', '\_');
BEGIN
    -- The same gate as search_object_ids: this tenant's session, or one
    -- allowed across tenants.
    IF p_tenant_id IS NULL
       OR NOT (p_tenant_id IS NOT DISTINCT FROM public.paladin_session_tenant_id()
               OR public.paladin_session_cross_tenant()) THEN
        RETURN;
    END IF;
    IF p_delimiter IS NULL OR p_delimiter = '' OR p_limit IS NULL OR p_limit < 1 THEN
        RETURN;
    END IF;
    IF p_state IS NOT NULL
       AND NOT p_state = ANY (enum_range(NULL::public.object_state)::text[]) THEN
        RETURN;
    END IF;
    SELECT c.id INTO v_collection_id
      FROM public.collections c
     WHERE c.tenant_id = p_tenant_id AND c.name = p_collection;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    -- Every key under the prefix is read once and folded to its first
    -- segment. Sorted bytewise, as keys are everywhere else.
    RETURN QUERY
    SELECT f.folder
      FROM (SELECT DISTINCT
                   (v_prefix
                    || split_part(substr(o.path, char_length(v_prefix) + 1), p_delimiter, 1)
                    || p_delimiter) COLLATE "C" AS folder
              FROM public.objects o
             WHERE o.tenant_id = p_tenant_id
               AND o.collection_id = v_collection_id
               AND o.path LIKE v_like || '%'
               AND starts_with(o.path, v_prefix)
               AND strpos(substr(o.path, char_length(v_prefix) + 1), p_delimiter) > 0
               AND (p_state IS NULL OR o.state = p_state::public.object_state)
               AND (NOT p_live_only OR o.state <> 'DELETED'::public.object_state)) f
     ORDER BY f.folder
     LIMIT p_limit;
END
$$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS paladin_key_successor(text);
