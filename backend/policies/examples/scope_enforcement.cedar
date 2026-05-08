// scope_enforcement.cedar — ASPIRATIONAL.
//
// This policy is NOT loaded by default. It documents the planned
// scope-enforcement model — a universal forbid template that rejects any
// request whose `resource` does not match a Scope on the principal — but
// requires a Cedar engine extension that has not landed yet.
//
// What's missing:
//   - cedar.Principal currently exposes Subject, TenantID, Roles. It does
//     NOT carry Scopes, and buildEntities (internal/policy/cedar/engine.go)
//     does not emit a `scopes` attribute on the principal-User entity.
//   - auth.Principal already has a Scopes field on the JWT side; the
//     adapter needs to convert auth.Scope → []string and include it when
//     constructing cedar.Principal at every handler call site.
//
// Loading this policy today against the live engine would fail policy
// evaluation with "attribute scopes not found" because the principal-User
// entity lacks the field. Tracked separately; do not enable in production.
//
// Scope strings live on User.scopes / ApiKey.scopes as Set<String>:
//   "tenant:<uuid>"           — tenant-wide
//   "backend:<id>"            — any resource under that backend
//   "bucket:<bucket_name>"    — any resource under that bucket
//   "object_key:<bucket>/<ok>"— any resource under that object_key
//   wildcard "*"              — escalation; reserved for platform admins

forbid (
  principal,
  action,
  resource
)
when { !( "*" in principal.scopes ) }
unless {
  ( resource has tenant_id   && ("tenant:"   + resource.tenant_id)   in principal.scopes ) ||
  ( resource has backend_id  && ("backend:"  + resource.backend_id)  in principal.scopes ) ||
  ( resource has bucket_name && ("bucket:"   + resource.bucket_name) in principal.scopes ) ||
  ( resource has object_key  && resource has bucket_name &&
    ("object_key:" + resource.bucket_name + "/" + resource.object_key) in principal.scopes )
};
