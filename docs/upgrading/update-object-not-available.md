## Unreleased — updating an object that is not AVAILABLE is FAILED_PRECONDITION

- `UpdateObject`, and the `PutObjectTags` / `DeleteObjectTags` calls built on
  it, on an object in the trash or still uploading now answer
  `FAILED_PRECONDITION`, as `DeleteObject` already does. They used to answer
  `ABORTED` with `ERROR_REASON_VERSION_CONFLICT`, which told a client to
  re-read and retry an update that could never land. `ABORTED` is still the
  answer for a stale `resource_version` on an AVAILABLE object.
