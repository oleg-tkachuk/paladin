## sdk/python/v0.151.5, sdk/go/v0.151.5 — an object key is bounded in length and text

- An object key is at most 731 bytes of UTF-8, in segments of at most 255
  bytes, without control characters (Unicode category Cc: U+0000–U+001F,
  U+007F, U+0080–U+009F). `UploadObject`, `InitiateMultipartUpload` and
  `CopyObject` (`destination_key`) refuse any other key as
  `INVALID_ARGUMENT`, naming the rule and the reason with the limit. Such a
  key used to be accepted and then fail at the store, after the object's
  row was written.
- 731 is S3's 1024-byte path less the tenant id, the longest collection a
  name may be, and the two separators, so a key fits in every collection
  and stays valid wherever it is copied. 255 is the longest file name a
  filer-backed store (SeaweedFS) takes; it refuses a longer segment with
  `KeyTooLongError` whatever the path's length.
- `BatchCopyObjects` refuses a `destination_key_prefix` that leaves no room
  for a key: 731 bytes or more, a segment before its last `/` over 255
  bytes, or 255 bytes or more after it. A copy whose prefix and source key
  together break the rule fails that item, with the reason in the
  operation's failures.
- Objects stored before this release under a key that breaks only these
  rules stay readable and deletable, and storage ingest still promotes them:
  the storage path refuses only what would share another object's path or
  what the store cannot hold (over 1024 bytes, or not UTF-8). A copy of one
  needs a `destination_key` that keeps the rule.
- Both SDKs' fakes (`paladintest`, `paladin.testing`) refuse the same keys,
  held to the server by `max_key_bytes`, `max_segment_bytes` and the
  `lengths` cases of `sdk/testdata/object_keys.json`.
