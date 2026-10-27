## Unreleased — policy history names older writers, and agents can read it

- **Migration `103`** fills in `written_by_tenant_id` for the versions
  written before `102` whose `written_by` is a user's id; a writer that is no
  user stays without one. Nothing to do.
