# Security

What the backend enforces, and where. The scope and the known non-findings are
in [docs/security-model.md](../../docs/security-model.md); reporting a
vulnerability is in [SECURITY.md](../../.github/SECURITY.md).

## 1. Authentication

Every RPC carries one of four credentials. Each resolves to a `Principal` with
a kind, a tenant and a role set.

| Credential | Format | Issued by | Notes |
|---|---|---|---|
| User access token | JWT, HS256, one per audience (`paladin-data`, `paladin-admin`, `paladin-iam`) | `AuthService.Login`, `RefreshToken`, `ExchangeAudience`, `SwitchTenant` | signed with `auth.signing_key` (`internal/auth/issuer`); with `auth.jwks_url` set the planes verify against that JWKS instead |
| Refresh token | JWT, HS256, audience `paladin-iam` | `Login`, rotation in `RefreshToken` | stored by jti in `refresh_tokens`; rotation and reuse detection below |
| API token | opaque, `TokenPrefix` + random body (`internal/auth/api_token/format.go`) | `APITokenService.Create` | stored as a keyed SHA-256 digest; per-token rate limit in `api_token_rate_buckets` |
| Capability token | JWT, EdDSA (Ed25519), `typ: paladin-cap+jwt`, or its Biscuit form; in `X-Paladin-Capability` or `Authorization: Capability <token>` | `CapabilityService.Issue` | budgeted, delegable only by narrowing, revocable (a Biscuit copy can be revoked alone); a key-bound capability (`cnf.jkt`) also needs a DPoP proof; the [`capability`](../../capability/) module |

For MCP clients, the api role serves an OAuth 2.1 authorization server under
`/oauth/*` ([ADR-0009](../../docs/adr/0009-oauth-authorization-server.md)) and
the mcp role validates its tokens as a resource server
([ADR-0008](../../docs/adr/0008-mcp-oauth-resource-server.md)).

**Refresh-token rotation.** Every rotation supersedes the presented token and
mints a successor in the same family. Presenting a superseded token is:

- inside the 30-second supersession grace: a race between two requests; the
  loser is told `Aborted` and derives an access token with `ExchangeAudience`;
- within 10 minutes, if the successor was never presented: a rotation whose
  response was lost; the successor is re-issued (same jti);
- otherwise: a replay; the whole family is revoked and an
  `iam.RefreshTokenReuseDetected` audit entry is written.

Logout and reuse detection clear `superseded_at`, so a deliberately ended
session is never covered by either window (`internal/api/iam/v1/authh`).

Passwords are bcrypt hashes (`internal/auth/password.go`).

## 2. Authorization and isolation

Applied in order on every request:

1. **Cedar policy.** Platform defaults in [`backend/policies/`](../policies/),
   tenant policies in the database. Authoring guide:
   [cedar-authoring.md](cedar-authoring.md).
2. **Scope.** A principal's scopes (`tenant:`, `backend:`, `bucket:`,
   `collection:`; `internal/auth/scope.go`) narrow what it reaches, and a
   capability's caveats narrow further: an operation set, resource prefixes or
   URIs, source CIDRs, request and budget ceilings, tainted-object reads and a
   required idempotency key (`capability/types.go`).
3. **Row-level security.** Tenant tables are `FORCE ROW LEVEL SECURITY`. The
   runtime role `paladin_app` is `NOBYPASSRLS`; each transaction sets
   `paladin.tenant_id` (or `paladin.cross_tenant` for an authorised
   cross-tenant read) and a missing setting yields zero rows. Background jobs
   use a separate BYPASSRLS pool. Roles: [db-roles.md](db-roles.md).

The tenant comes from the credential, never from a request header. The one
header that names a tenant, `X-Tenant-Id`, is a disambiguation hint for
`AuthService.Login`, which still requires the password.
`security.reject_tenant_mismatch` is accepted by the loader but read by
nothing.

## 3. Storage

- **Presigned URLs.** Clients never receive storage credentials. The api plane
  signs SigV4 URLs for single objects. A URL cannot be revoked before it
  expires, so its lifetime is policy: `put_ttl`, `get_ttl` and `part_ttl`
  under `limits.presign` are the defaults, and a caller asking for more than
  `max_ttl` (at most 168h, the SigV4 ceiling) is refused with
  `InvalidArgument` rather than shortened.
- **Server-side encryption.** Per backend, `storage.backends.<name>.sse.type`
  selects none, `AES256` or `aws:kms` (with `sse.key_id`); the S3 adapter sets
  it on writes.

## 4. Input validation

- **Request shape.** Connect handlers run behind a `protovalidate` interceptor
  (`internal/middleware/validate.go`); a request violating its
  `buf.validate` rules is refused with `InvalidArgument` before the handler.
- **Upload limits.** Every path that creates an object — UploadObject,
  multipart, copy, batch copy — is admitted by one policy
  (`internal/uploadpolicy`). `limits.max_object_size` bounds a single PUT or
  POST, `limits.max_multipart_size` an object assembled from parts, and
  `min_part_size`, `max_part_size` and `max_parts` decide the part plan.
  `limits.allowed_content_types` (empty accepts any; parameters are ignored)
  refuses other media types. A bucket's constraints narrow all of these —
  never widen them — and may also require a checksum algorithm and cap the
  bucket's presign TTLs. Constraints no upload could satisfy are refused when
  the bucket is created.

## 5. Secret Management

At boot the `K8sSecretResolver` reads the pod's ServiceAccount token and
resolves every `*_secret` / `*_ref` field in the config to its plaintext
value in-memory, then clears the ref. The resolvable references are:

- `datastores.postgres.password_secret` (runtime role),
  `migrate_password_secret` (DDL role), `reaper_password_secret` (cross-tenant
  DML role) and `replica.password_secret` (when the replica is enabled)
- `bootstrap.admin.password_secret` (when the step is enabled)
- `storage.backends.<name>.auth.{access_key,secret_key,session_token}_secret`
- `auth.signing_key_secret` — the HMAC key that signs + verifies every JWT
- `api_token.hmac_key_secret` — the server-side key of the API-token digest
- `runtime.health_snapshot_token_secret` — the `/system/health.json` token
- `ingest.webhook.shared_secret_ref` — the storage-event webhook HMAC key

Every referenced Secret name must appear in
`rbac.secretReader.secretNames` (the chart appends the ones it wires itself:
the Postgres passwords, the S3 credentials, the bootstrap admin and the
generated signing key); a missing name surfaces as a **403 at boot**, not a
silent skip. Out-of-cluster (no SA token) the resolver no-ops and inline
values are used instead. An inline value and its `*_secret` reference (`signing_key` /
`signing_key_secret`, `password` / `password_secret`, and the webhook's inline
vs ref) are mutually exclusive — setting both is a startup error.

### SealedSecrets (kubeseal) runbook

For prod-class clusters keep no key material inline in Helm values. Seal each
secret with [Bitnami SealedSecrets](https://github.com/bitnami-labs/sealed-secrets)
so the encrypted form is safe to commit to git; the controller unseals it into a normal Secret in the
namespace, which the resolver then reads.

Seal a value with `kubeseal --raw` (scoped to the target namespace + Secret
name, so it can't be reused elsewhere):

```bash
# 1. Auth signing key → Secret paladin-auth-signing-key, key `signing_key`
openssl rand -hex 32 | kubeseal --raw \
  --namespace paladin --name paladin-auth-signing-key \
  --scope strict --from-file=/dev/stdin
# → paste the ciphertext into the SealedSecret's spec.encryptedData.signing_key

# 2. Ingest webhook HMAC → Secret paladin-ingest-hmac, key `secret`
printf '%s' "$WEBHOOK_HMAC" | kubeseal --raw \
  --namespace paladin --name paladin-ingest-hmac \
  --scope strict --from-file=/dev/stdin
```

Then in the prod overlay point the config at the unsealed Secret and
allowlist its name (see `values-prod.yaml`):

```yaml
config:
  auth:
    signing_key: ""                 # empty — resolved from the ref below
    signing_key_secret: { name: paladin-auth-signing-key, key: signing_key }
rbac:
  secretReader:
    secretNames: [ paladin-auth-signing-key, paladin-ingest-hmac ]   # + the base list
```

The same pattern applies to the Postgres, bootstrap-admin, and storage
credential Secrets — seal each, reference it by `*_secret`, allowlist the
name. The SealedSecrets controller and the sealed YAML live outside this
repository.

## 6. Bootstrap admin (ArgoCD-style)

A fresh install has no users. The chart's bootstrap Job provisions a platform
admin from a Kubernetes Secret, the way ArgoCD's `argocd-initial-admin-secret`
works. Both switches are on in the chart by default: `bootstrap.admin.enabled`
(the chart creates the Secret) and `config.bootstrap.admin.enabled` (the Job
runs the step). A GitOps install that ships its own Secret sets the first to `false`.

The Helm chart **creates the Secret itself** — operators don't pre-create
it. The password is resolved by the same `K8sSecretResolver` that handles
the postgres password, so there is no env-var or `secretKeyRef` on the
deployment.

### Install (auto-generated password)

```bash
helm install paladin ./deploy/chart

# Retrieve the auto-generated password:
kubectl get secret paladin-bootstrap-admin -n paladin \
  -o jsonpath='{.data.password}' | base64 -d
```

The chart picks one of three values for the Secret content, in priority
order:

1. **Existing Secret in the namespace** — preserved as-is (`lookup` +
   `helm.sh/resource-policy: keep`). Helm upgrades don't rotate it.
2. **Operator-supplied** via `--set bootstrap.admin.password=…`. Use
   `--set-file` to read from disk and keep it out of shell history.
3. **Auto-generated** 24-char `randAlphaNum` (default).

### Install with an explicit password

```bash
echo -n "$NEW_PASSWORD" > /tmp/admin.pw
helm install paladin ./deploy/chart \
  --set-file bootstrap.admin.password=/tmp/admin.pw
shred -u /tmp/admin.pw
```

### What the boot step does

The `bootstrap` subcommand runs as a `pre-install,pre-upgrade` hook Job after
the migrate Job (hook weight `10` against `0`), before the Deployments roll
out. It calls
[`internal/bootstrap.EnsureAdmin`](../internal/bootstrap/admin.go), which:

1. Reads `cfg.Bootstrap.Admin.Password`. In-cluster the value is filled
   by `K8sSecretResolver` from the Secret coordinates in
   `bootstrap.admin.password_secret`. Locally the operator may set
   `bootstrap.admin.password` inline (debug mode only).
2. Creates the dedicated tenant (`platform` by default) if absent.
   Tagged `managed_by=paladin-bootstrap` for grep-ability.
3. Hashes the password with bcrypt and inserts the `admin` user into
   `users` with role `platform.admin` (cross-tenant via Cedar).
4. Writes an audit entry under action `iam.bootstrap_admin.create`.

The step is **idempotent**: subsequent runs see the user already exists
and skip (logged at INFO).

### Password rotation

```bash
# 1. Patch the Secret in place — operator-controlled value:
kubectl patch secret paladin-bootstrap-admin -n paladin \
  -p '{"data":{"password":"'"$(printf '%s' "$NEW_PASSWORD" | base64)"'"}}'

# 2. Trigger the boot step to rotate via a one-shot upgrade:
helm upgrade paladin ./deploy/chart \
  --reuse-values \
  --set config.bootstrap.admin.force_reset=true

# 3. After the rollout completes, flip the flag back so the bootstrap Job
#    doesn't re-hash on every upgrade (logged WARN; harmless but noisy):
helm upgrade paladin ./deploy/chart \
  --reuse-values \
  --set config.bootstrap.admin.force_reset=false
```

The rotation path writes audit `iam.bootstrap_admin.reset` and logs a
WARN with the user_id. The same flow handles "I lost the admin password"
DR scenarios.

### Threat model notes

- The bootstrap password is **never** stored in YAML or in Helm release
  values when auto-generated — only in the Secret in etcd. The
  `password` value is empty by default; the chart populates the Secret
  body with `randAlphaNum` at template-render time.
- `helm.sh/resource-policy: keep` on the Secret protects the admin
  credential from being deleted by `helm uninstall`. Removing the user
  requires explicit `kubectl delete secret`.
- `force_reset=true` rotates `password_hash` but does **not** revoke
  existing refresh tokens for the admin. Operators MUST also call
  `AuthService.Revoke` (or wait out the TTL) if they suspect compromise.
- The admin user is regular thereafter — it can be deleted, renamed via
  `UpdateUser`, or have its password rotated through `ChangePassword`.
  The bootstrap step doesn't enforce its continued existence.
