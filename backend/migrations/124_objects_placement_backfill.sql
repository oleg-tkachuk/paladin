-- +goose Up
-- +goose StatementBegin

-- Until now every object with confirmed bytes had them in its collection's
-- bucket: that is where every writer put them, and a storage migration copied
-- them there before it moved the binding (ADR-0015). A PENDING row keeps NULL;
-- its promote places it.
UPDATE objects o
   SET placement_bucket_id = c.bucket_id
  FROM collections c
 WHERE c.id = o.collection_id
   AND o.state <> 'PENDING'
   AND o.placement_bucket_id IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE objects SET placement_bucket_id = NULL WHERE placement_bucket_id IS NOT NULL;
-- +goose StatementEnd
