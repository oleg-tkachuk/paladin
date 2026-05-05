// scope_enforcement.cedar — universal forbid template that ensures every
// request's resource matches the principal's scope set. Loaded with the
// default policy stack on every tenant; do not remove.
//
// Scope strings live on User.scopes / ApiKey.scopes as set<String>:
//   "tenant:<uuid>"           — tenant-wide
//   "backend:<id>"            — any resource under that backend
//   "bucket:<bucket_name>"    — any resource under that bucket
//   "object_key:<bucket>/<ok>"— any resource under that object_key
//   wildcard "*"              — escalation; reserved for platform admins
//
// Resources expose backend_id / bucket_name / object_key / tenant_id
// directly; the rule below short-circuits the wildcard case first then
// requires at least one specific match.

forbid (
  principal,
  action,
  resource
)
when { !( "*" in principal.scopes ) }
unless {
  ( resource has tenant_id     && ("tenant:"     + resource.tenant_id)     in principal.scopes ) ||
  ( resource has backend_id    && ("backend:"    + resource.backend_id)    in principal.scopes ) ||
  ( resource has bucket_name   && ("bucket:"     + resource.bucket_name)   in principal.scopes ) ||
  ( resource has object_key    && resource has bucket_name &&
    ("object_key:" + resource.bucket_name + "/" + resource.object_key) in principal.scopes )
};
