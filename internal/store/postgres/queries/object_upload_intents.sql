-- Object upload intent queries
--
-- Intents hold the server-generated upload intent produced by UploadObject
-- until the client confirms success via CompleteObject. No object exists in
-- the `objects` table while an intent is outstanding; on completion the
-- intent is deleted and an object row is INSERTed in the same transaction.

-- name: CreateUploadIntent :exec
INSERT INTO object_upload_intents (
    id, tenant_id, bucket, object_key, category, subpath,
    content_type, size_bytes, labels, tags, external_ref,
    idempotency_key, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
);

-- name: GetUploadIntent :one
SELECT sqlc.embed(object_upload_intents)
FROM object_upload_intents
WHERE tenant_id = $1 AND id = $2;

-- name: GetUploadIntentByKey :one
SELECT sqlc.embed(object_upload_intents)
FROM object_upload_intents
WHERE tenant_id = $1 AND bucket = $2 AND object_key = $3;

-- name: GetUploadIntentByIdempotencyKey :one
SELECT sqlc.embed(object_upload_intents)
FROM object_upload_intents
WHERE tenant_id = $1 AND idempotency_key = $2;

-- name: DeleteUploadIntent :execrows
DELETE FROM object_upload_intents
WHERE tenant_id = $1 AND id = $2;

-- name: DeleteExpiredUploadIntents :execrows
DELETE FROM object_upload_intents AS oui
WHERE oui.id IN (
    SELECT inner_oui.id
    FROM object_upload_intents AS inner_oui
    WHERE inner_oui.expires_at < $1
    LIMIT $2
);
