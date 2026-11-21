## Unreleased — a batch tag update refuses an object that is not AVAILABLE

- `BatchUpdateTags` reported an object in the trash or still uploading as
  `resource_version mismatch`, after trying it three times. Its failure now
  says `failed_precondition: object state <STATE> takes no metadata update`,
  the reason `UpdateObject` gives, and the object is tried once.
