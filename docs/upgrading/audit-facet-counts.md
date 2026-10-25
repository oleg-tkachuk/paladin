## v40.1.0, sdk/go/v0.119.0, sdk/python/v0.120.0 — the audit log narrows and counts by plane, outcome and service

- **`ListAuditLogRequest` gains `facets` and `count_facets`**. `facets`
  (`AuditFacets`) narrows the log to the planes (`actor_audience`), outcomes
  (`AuditOutcome`) and services it names; with `count_facets` the first
  page's answer carries `facet_counts`, each facet counted without its own
  choices. An entry's service is its action's `pkg.Service`, or the action
  itself for one of the server's own. Additive.
- **Migration `100`** replaces `list_audit_entry_ids` with a version taking
  the facets and adds `audit_service`; nothing to do.
- The console's audit log filters by plane, outcome and service with counts.
