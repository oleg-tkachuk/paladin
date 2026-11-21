## Unreleased — every paladin.object.purged names the backend as `backend_id`

- A `paladin.object.purged` event from a lifecycle hard delete (`source:
  lifecycle`) carried the storage backend under `backend`; it now carries it
  under `backend_id`, as a permanent delete's and the purge drainer's always
  did. A subscriber reading `backend` on lifecycle purges reads `backend_id`.
