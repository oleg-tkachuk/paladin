-- +goose Up
-- +goose StatementBegin

-- Where an object's bytes are (placement_bucket_id) and which bucket a URL of
-- it can still write (upload_bucket_id) are Paladin's bookkeeping (ADR-0051):
-- a storage migration's move and the reconciler's settle of an expired URL
-- change them under a client that has changed nothing. An update of those
-- alone keeps the row's resource_version, so a client's compare-and-set made
-- with the version it last read still applies. Any other change bumps it as
-- before (003).
DROP TRIGGER objects_bump_resource_version ON objects;
CREATE TRIGGER objects_bump_resource_version BEFORE UPDATE ON objects
    FOR EACH ROW
    WHEN ((to_jsonb(OLD) - 'placement_bucket_id' - 'upload_bucket_id')
          IS DISTINCT FROM (to_jsonb(NEW) - 'placement_bucket_id' - 'upload_bucket_id'))
    EXECUTE FUNCTION bump_resource_version();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER objects_bump_resource_version ON objects;
CREATE TRIGGER objects_bump_resource_version BEFORE UPDATE ON objects
    FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd
