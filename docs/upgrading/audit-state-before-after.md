## v39.4.0, sdk/go/v0.116.0, sdk/python/v0.117.0 — an audit entry records the resource before and after

- **`AuditLogEntry` carries `state_before_json` and `state_after_json`**:
  the resource an admin action changed, as its own Get read it just before
  and just after the action, secret fields redacted. A create records only
  the after — its answer — and a delete only the before. The reads are not in
  the action's transaction, so a concurrent change can fall between them.
  The deprecated `before_json` and `after_json` stay empty.
- **Migration `099`** adds the two columns to `audit_log`; nothing to do.
- The console's audit entry gains a Changes tab listing the fields that
  changed.
