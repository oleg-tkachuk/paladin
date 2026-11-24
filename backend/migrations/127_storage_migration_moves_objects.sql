-- +goose Up
-- +goose StatementBegin

-- A storage migration moves objects one by one (ADR-0051). It no longer
-- rebinds at an instant: on entering 'moving' it points new uploads at the
-- target, and each pass moves the objects placed in the source whose upload
-- anchor has settled.
--
-- writes_close_at: when the last URL that can write the bucket being emptied
-- (the source, or the target while rolling back) expires. Set by the first
-- pass of each direction, now plus limits.presign.max_ttl; a regenerated or
-- part URL for that bucket is signed to expire by it.
-- failed_passes: the passes in a row whose moves failed their verification;
-- past migstate.MaxFailedPasses the migration rolls back.
ALTER TABLE tenant_storage_migrations ADD COLUMN writes_close_at timestamptz;
ALTER TABLE tenant_storage_migrations ADD COLUMN failed_passes integer NOT NULL DEFAULT 0;

-- The old worker's states between provisioning and completed resume as
-- 'moving': each pass points whatever is still bound to the source at the
-- target, so a migration that had not rebound yet is taken from where it is.
UPDATE tenant_storage_migrations
   SET state = 'moving', updated_at = now()
 WHERE state IN ('copying', 'rebinding', 'verifying');

-- An upload's anchor now outlives its promote, until its URL expires and the
-- reconciler settles it. Every pending upload has one: a row from before 113
-- is anchored to its collection's bucket, where its URL was signed. A
-- promoted object whose URL may still write is anchored where it is placed.
UPDATE objects o
   SET upload_bucket_id = c.bucket_id
  FROM collections c
 WHERE c.id = o.collection_id
   AND o.state = 'PENDING'
   AND o.upload_bucket_id IS NULL;
UPDATE objects
   SET upload_bucket_id = placement_bucket_id
 WHERE state <> 'PENDING'
   AND upload_bucket_id IS NULL
   AND placement_bucket_id IS NOT NULL
   AND presign_expires_at > now();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE objects SET upload_bucket_id = NULL WHERE state <> 'PENDING';
UPDATE tenant_storage_migrations SET state = 'copying' WHERE state = 'moving';
ALTER TABLE tenant_storage_migrations DROP COLUMN failed_passes;
ALTER TABLE tenant_storage_migrations DROP COLUMN writes_close_at;
-- +goose StatementEnd
