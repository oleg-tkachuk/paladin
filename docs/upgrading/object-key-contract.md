## Unreleased — every object has a storage path of its own

- A nested collection occupies one segment of the bucket path: its `/` is
  written `.`. Collection `invoices/2026/q1` stores key `report.pdf` at
  `<tenant>/invoices.2026.q1/report.pdf`, where it used to share
  `<tenant>/invoices/2026/q1/report.pdf` with key `2026/q1/report.pdf` of
  collection `invoices`. Collections without a `/` keep their layout.
  `Object.placement.storage_path` reports the new path.
- Objects of nested collections uploaded before this release are still
  listed, but their bytes are where the old layout put them, so reading them
  fails. Delete such objects and upload them again; on a cluster whose data
  is disposable, delete the nested collections and create them again. Their
  old bytes are not removed by Paladin.
- An object key is segments separated by `/`, none of them empty, `.` or
  `..`: no leading or trailing `/`, no `//`. `UploadObject`,
  `InitiateMultipartUpload` and `CopyObject` (`destination_key`) refuse any
  other key as `INVALID_ARGUMENT`, naming the rule and the reason. Such keys
  used to be stored as given while their storage path was normalised, so
  two objects of one tenant could share a path. A key ending in `/` is
  refused too: a filer-backed store keeps it as a directory and drops the
  bytes written to it.
- `BatchCopyObjects` refuses a `destination_key_prefix` with a leading `/`,
  or an empty, `.` or `..` segment before its last `/`, as
  `INVALID_ARGUMENT`. A copy whose prefix and source key together are not a
  canonical key fails that item, with the reason in the operation's
  failures.
- `CopyObject` without a `destination_key` keeps the source's key only if
  it is canonical; otherwise it is `INVALID_ARGUMENT`, and the copy needs a
  key of its own.
- `InitiateMultipartUpload` without a `key` names one, as `UploadObject`
  does. It used to store the object at the collection's own prefix.
- Objects stored under a key that breaks the rule before this release stay
  listed, but reading their bytes fails, and their placement is not
  reported. Deleting them settles without deleting bytes, which may belong
  to the object whose key the old composer rewrote theirs to. Find them
  with:

  ```sql
  SELECT tenant_id, collection_id, id, path FROM objects
  WHERE path = '' OR path LIKE '/%' OR path LIKE '%/'
     OR path LIKE '%//%' OR path ~ '(^|/)\.\.?(/|$)'
  ```

  then delete each through the API, or, where the data is disposable,
  delete the rows.
