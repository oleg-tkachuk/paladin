## Unreleased — a tenant's slug stays reserved after trash and purge

- **Creating a tenant refuses a slug a trashed tenant holds, or a purged
  tenant held**, with `AlreadyExists` and reason `SLUG_TAKEN`. Before, a
  trashed tenant's slug was free to take, and restoring the trashed tenant
  then failed. Set `admin.tenants.reserve_slugs: false` to keep the old
  behaviour and allow reuse after purge.
- **A purge records the slug in the new `retired_tenant_slugs` table**,
  whatever the setting. Slugs purged before this release were not recorded
  and stay free.
- **The slug can no longer be updated in the database, not even with
  `paladin.allow_slug_rename`**; the trigger ignores that setting now.
