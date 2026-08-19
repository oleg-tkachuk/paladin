# Security - Paladin

## Overview

Paladin is designed with a "Secure by Default" mindset, focusing on tenant isolation and least-privilege access.

## 1. Authentication

Access is controlled via two primary mechanisms:

- **Tenant Context**: All requests must be associated with a valid `TenantId`. In production, this is usually extracted from a JWT or set by an upstream reverse proxy.
- **Admin Authentication**: Administrative endpoints (e.g., `/admin/config`) are protected by a shared secret (`auth.admin_key`).

## 2. Authorization & Isolation

- **Tenant Scoping**: All database queries and storage operations are strictly scoped by `tenant_id`.
- **Reject Tenant Mismatch**: If enabled (`security.reject_tenant_mismatch`), Paladin will reject any request where the derived tenant ID doesn't match the one explicitly provided in the request body or path.
- **RLS (Planned)**: Future support for PostgreSQL Row Level Security to provide an additional layer of isolation at the database level.

## 3. Storage Security

- **Signed URLs**: Clients never get direct access to storage credentials. Paladin issues time-limited pre-signed URLs (HMAC) for specific objects.
- **SSE (Server Side Encryption)**: Paladin supports AES-256 or KMS-based encryption for objects at rest in S3/SeaweedFS.

## 4. Input Validation

- **JSON Schema**: All REST request bodies are validated against the OpenAPI specification.
- **Content Type Enforcement**: Paladin rejects uploads with content types not in the `allowed_content_types` whitelist.
- **Size Limits**: Enforced at the control plane layer (`max_object_size`) and propagated to S3 via pre-signed URL conditions.

## 5. Secret Management

At boot the `K8sSecretResolver` reads the pod's ServiceAccount token and
resolves every `*_secret` / `*_ref` field in the config to its plaintext
value in-memory, then clears the ref. The resolvable references are:

- `datastores.postgres.password_secret` (runtime role) and
  `migrate_password_secret` (DDL role)
- `bootstrap.admin.password_secret`
- `storage.backends.<name>.auth.{access_key,secret_key,session_token}_secret`
- `auth.signing_key_secret` — the HMAC key that signs + verifies every JWT
- `ingest.webhook.shared_secret_ref` — the storage-event webhook HMAC key

Every referenced Secret name must appear in
`rbac.secretReader.secretNames` (the chart auto-appends the primary-storage
credential Secret); a missing name surfaces as a **403 at boot**, not a
silent skip. Out-of-cluster (no SA token) the resolver no-ops and inline
values are used instead. `signing_key` / `signing_key_secret` (and the
webhook's inline vs ref) are mutually exclusive — setting both is a startup
error.

### SealedSecrets (kubeseal) runbook

For prod-class clusters keep no key material inline in Helm values. Seal each
secret with [Bitnami SealedSecrets](https://github.com/bitnami-labs/sealed-secrets)
so the encrypted form is safe to commit to git (in `gitops`, alongside the
ArgoCD ApplicationSet); the controller unseals it into a normal Secret in the
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
name. The SealedSecrets **controller install** and the sealed YAML live in
`gitops`, not this repo.

## 6. Bootstrap admin (ArgoCD-style)

A fresh Paladin cluster has no users. To avoid the chicken-and-egg of "you
need an admin token to create the first user, but you need the first
user to mint a token", the server can provision a platform-admin on
first boot from a Kubernetes Secret — exactly like ArgoCD's
`argocd-initial-admin-secret`.

The Helm chart **creates the Secret itself** — operators don't pre-create
it. The password is resolved by the same `K8sSecretResolver` that handles
the postgres password, so there is no env-var or `secretKeyRef` on the
deployment.

### Install (auto-generated password)

```bash
helm install paladin ./deploy/chart \
  --set bootstrap.admin.enabled=true \
  --set config.bootstrap.admin.enabled=true

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
  --set bootstrap.admin.enabled=true \
  --set config.bootstrap.admin.enabled=true \
  --set-file bootstrap.admin.password=/tmp/admin.pw
shred -u /tmp/admin.pw
```

### What the boot step does

After migrations, before any listener accepts traffic,
[`internal/bootstrap.EnsureAdmin`](../internal/bootstrap/admin.go):

1. Reads `cfg.Bootstrap.Admin.Password`. In-cluster the value is filled
   by `K8sSecretResolver` from the Secret coordinates in
   `bootstrap.admin.password_secret`. Locally the operator may set
   `bootstrap.admin.password` inline (debug mode only).
2. Creates the dedicated tenant (`platform` by default) if absent.
   Tagged `managed_by=paladin-bootstrap` for grep-ability.
3. Hashes the password with bcrypt and inserts the `admin` user into
   `iam.users` with role `platform.admin` (cross-tenant via Cedar).
4. Writes an audit entry under action `iam.bootstrap_admin.create`.

The step is **idempotent**: subsequent boots see the user already exists
and skip silently (logged at INFO).

### Password rotation

```bash
# 1. Patch the Secret in place — operator-controlled value:
kubectl patch secret paladin-bootstrap-admin -n paladin \
  -p '{"data":{"password":"'"$(printf '%s' "$NEW_PASSWORD" | base64)"'"}}'

# 2. Trigger the boot step to rotate via a one-shot upgrade:
helm upgrade paladin ./deploy/chart \
  --reuse-values \
  --set config.bootstrap.admin.force_reset=true

# 3. After the rollout completes, flip the flag back so the server
#    doesn't re-hash on every restart (logged WARN; harmless but noisy):
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
