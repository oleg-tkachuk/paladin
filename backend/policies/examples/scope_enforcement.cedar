// scope_enforcement.cedar — SHIPPED (reference copy).
//
// Resource-scope enforcement has LANDED. The authoritative policy is the
// `forbid` block appended to `builtinPolicy` in
// internal/policy/cedar/engine.go — it is prepended to every compiled policy
// set, so it applies uniformly across all tenants (not a per-tenant policy).
// This file is the annotated reference copy; keep it in sync with the builtin.
//
// How it works (and why it differs from the original aspirational draft):
//
//   - cedar.Principal now carries Scopes []string, and buildEntities emits them
//     as the `scopes` Set<String> on the principal-User entity. The adapters
//     (apiutil.ScopeStrings / CedarPrincipal) convert auth.Scope → wire string
//     via Scope.String() on every data-plane authz call site.
//
//   - The set of scope-strings that ADMIT a resource is precomputed in Go
//     (engine.go: resourceScopeKeys) and stamped on every resource-side entity
//     as the `scope_keys` Set<String>. This is the ONE place the wire format is
//     produced, so it cannot drift from auth.Scope.String() / auth.MatchScope.
//
//   - Matching is therefore a pure set-intersection (`containsAny`). The
//     original draft tried to REBUILD the scope string inside Cedar with
//     `"tenant:" + resource.tenant_id` — but Cedar's `+` is integer addition,
//     not string concatenation, so that expression is a type error (the policy
//     would be skipped at eval and the forbid would fail-open). And membership
//     of a String in a Set<String> is `set.contains(x)`, NOT `x in set` (`in`
//     is the entity-hierarchy operator). Both bugs are fixed below.
//
// Scope wire strings (Set<String> on principal.scopes AND resource.scope_keys):
//   "tenant:<uuid>"               — tenant-wide
//   "backend:<id>"                — any resource under that backend
//   "bucket:<bucket_name>"        — any resource under that bucket
//   "object_key:<bucket>/<ok>"    — any resource under that object_key
//                                   (requires the physical bucket to be known)
//   wildcard "*"                  — escalation; reserved for platform admins
//
// Fires ONLY for principals with a NON-EMPTY scopes set and no "*". Principals
// with empty scopes (JWT users, roles-only callers, unscoped PATs) and
// wildcard-scoped principals are completely unaffected — the `when` guard is
// false for them, so the forbid never applies.

forbid (
  principal,
  action,
  resource
)
when {
  principal has scopes &&
  !principal.scopes.isEmpty() &&
  !principal.scopes.contains("*")
}
unless {
  resource has scope_keys &&
  principal.scopes.containsAny(resource.scope_keys)
};
