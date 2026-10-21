-- +goose Up

-- 096's count for the batches that finished before it, from the `failed` of
-- their response. A response that is not a batch's JSON keeps 0. The table
-- holds only the operations the reaper has not removed yet.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    n bigint;
BEGIN
    FOR r IN SELECT id, response FROM operations WHERE state = 'SUCCEEDED' AND response IS NOT NULL LOOP
        BEGIN
            n := (convert_from(r.response, 'UTF8')::jsonb ->> 'failed')::bigint;
        EXCEPTION WHEN others THEN
            n := NULL;
        END;
        IF n > 0 THEN
            UPDATE operations SET failed_count = n WHERE id = r.id;
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- 096's down drops the column with its counts.
SELECT 1;
