# Installing on Kubernetes

Two Helm charts, published to GHCR with every release:

| Chart | What it runs |
| --- | --- |
| `oci://ghcr.io/oleg-tkachuk/charts/paladin-core` | the backend: api, admin, worker, mcp and dispatcher Deployments, plus the migrate and bootstrap Jobs |
| `oci://ghcr.io/oleg-tkachuk/charts/paladin-console` | the web console and its BFF |

The charts do not run PostgreSQL or the object store. Bring both.

## Prerequisites

- Kubernetes 1.25+ and Helm 3.8+ (OCI charts).
- PostgreSQL 16+, reachable from the cluster.
- An S3-compatible object store — SeaweedFS, MinIO, Garage or AWS S3 — and an
  access key that may create buckets: Paladin creates the buckets it uses.
- Optional: an ingress controller. The charts' Ingress defaults to the
  [Caddy ingress controller](https://github.com/caddyserver/ingress).

## 1. Database roles

Paladin connects as two roles and grants a third. Run this once as a superuser;
replace the passwords.

```sql
-- Owns the schema and runs migrations. BYPASSRLS: migrations and the
-- cross-tenant maintenance work must see every tenant's rows.
CREATE ROLE paladin_migrate WITH LOGIN PASSWORD '<migrate-password>' BYPASSRLS;

-- The runtime role: DML only, and subject to row-level security.
CREATE ROLE paladin_app WITH LOGIN PASSWORD '<app-password>';

-- The worker's cross-tenant DML role. NOLOGIN until you give it a DSN
-- (config.datastores.postgres.reaper_dsn); the migrations grant it anyway.
CREATE ROLE paladin_reaper WITH NOLOGIN BYPASSRLS;

CREATE DATABASE paladin OWNER paladin_migrate;
GRANT CONNECT ON DATABASE paladin TO paladin_app;
```

Only a superuser can grant `BYPASSRLS`, which is why the migrations do not
create these roles themselves when they run as `paladin_migrate`. Without it
on `paladin_migrate` and `paladin_reaper`, migrations fail on the first
row-level-security policy.

## 2. Secrets

In the namespace you install into:

```bash
kubectl create namespace paladin
kubectl -n paladin create secret generic paladin-postgres-app --from-literal=password='<app-password>'
kubectl -n paladin create secret generic paladin-postgres-migrate --from-literal=password='<migrate-password>'
kubectl -n paladin create secret generic paladin-s3 \
  --from-literal=access_key='<access-key>' --from-literal=secret_key='<secret-key>'
```

The keys are `password`, `access_key` and `secret_key` unless you name others
in the values.

## 3. Values

The inputs the backend chart cannot default:

```yaml
# paladin-core.yaml
postgres:
  host: postgres.databases.svc.cluster.local
  sslmode: require
  app:
    existingSecret: paladin-postgres-app
  migrate:
    existingSecret: paladin-postgres-migrate

config:
  storage:
    backends:
      primary:
        endpoint: http://seaweedfs.storage.svc.cluster.local:8333
        public_endpoint: https://s3.example.com
        region: us-east-1

storage:
  s3CredentialsSecret:
    create: false
    existingSecret: paladin-s3
```

`public_endpoint` is the address clients reach the store by. Uploads and
downloads go straight from the client to the store over presigned URLs, and
those URLs are signed for this host. Leave it unset and they are signed for
`endpoint`, an in-cluster name no browser or SDK outside the cluster can
resolve. Set it to a host that reaches the same store, and the signature
covers the Host header, so a wrong value fails the signature rather than the
connection — see [Storage](../ARCHITECTURE.md#storage).

Without `postgres.host` (or a DSN written under `config.datastores.postgres`)
or without an endpoint and credentials for the object store, `helm install`
refuses to render and says which value is missing.

Everything else has a default:

- `config.app.env` is `prod`: weak secrets, ephemeral signing keys and an
  unauthenticated ingest webhook are refused at boot.
- The access-token signing key is generated into
  `<release>-paladin-core-auth-signing-key` and kept across upgrades. The
  issuer defaults to the release name.
- A platform admin is created on first install, with a generated password in
  the `paladin-bootstrap-admin` Secret.
- The Secrets the chart reads are allowed by a namespaced Role. If one lives in
  another namespace — `postgres.app.namespace`, say — set
  `rbac.secretReader.clusterWide: true`.

## 4. Install

```bash
helm install paladin-core oci://ghcr.io/oleg-tkachuk/charts/paladin-core \
  --version <version> -n paladin -f paladin-core.yaml
```

```bash
helm install paladin-console oci://ghcr.io/oleg-tkachuk/charts/paladin-console \
  --version <version> -n paladin
```

The migrate and bootstrap Jobs run before any Deployment starts. The console
finds the backend from its release name and namespace; if you named the backend
release something other than `paladin-core`, set `backend.release`, and
`backend.namespace` if it lives elsewhere.

The admin password:

```bash
kubectl -n paladin get secret paladin-bootstrap-admin -o jsonpath='{.data.password}' | base64 -d
```

Sign in as `admin`:

```bash
kubectl -n paladin port-forward svc/paladin-console 3000:3000
```

## Ingress

Both charts carry an optional Ingress. With the Caddy ingress controller:

```yaml
# paladin-core.yaml
ingress:
  enabled: true
  hosts: [api.paladin.example.com]
```

```yaml
# paladin-console.yaml
ingress:
  enabled: true
  hosts: [paladin.example.com]
```

Caddy obtains certificates itself; set `ingress.tls` to serve your own. The
backend Ingress routes `/paladin.iam.v1.*` to the IAM plane and everything else
to the data plane. The admin plane gets no public route: reach it through the
console, or expose it behind your own controls.

## Network policies

Both charts ship NetworkPolicies, on by default: every pod is denied in both
directions, then allowed the flows it needs. Without a CNI that enforces
policies (Calico, Cilium, kube-router) they change nothing. With one, each peer
must be named where it actually runs, or its traffic is dropped:

| Value (both charts unless noted) | Default | The peer |
|---|---|---|
| `networkPolicies.ingressController.namespace` / `.podLabels` | `networking`, `app.kubernetes.io/name: traefik` | the ingress controller |
| `networkPolicies.monitoring.namespace` (backend) | `monitoring` | the metrics scraper |
| `networkPolicies.postgres.namespace` / `.port` (backend) | `database`, `5432` | PostgreSQL |
| `networkPolicies.storage.namespace` (backend) | `storage` | the object store |
| `networkPolicies.nats.namespace` / `.port` (backend) | `nats`, `4222` | NATS, when ingest uses it |

With the Caddy ingress controller from the section above:

```yaml
# paladin-core.yaml and paladin-console.yaml
networkPolicies:
  ingressController:
    namespace: caddy-system   # where you installed it
    podLabels:
      app.kubernetes.io/name: caddy-ingress-controller
```

`networkPolicies.enabled: false` turns them off.

Another controller needs `ingress.className`, its own syntax for
`ingress.iam.path` (the IAM route has to match every path starting with
`/paladin.iam.v1.` — Kubernetes' `Prefix` type does not, as it compares whole
path segments), and its annotation for HTTPS to the backend when internal TLS
is on. Or leave `ingress.enabled` off and point your own ingress or gateway at
the `paladin-core-api` Service: port 8085 for `/paladin.iam.v1.*`, 8080 for the
rest.

## TLS between the planes

Off by default: the planes speak plain HTTP inside the cluster. To turn it on,
issue a certificate whose SANs name the release's Services
(`paladin-core-api`, `paladin-core-admin`, `paladin-core-mcp`) into a Secret
with `tls.crt`, `tls.key` and `ca.crt` — cert-manager can — and set:

```yaml
# paladin-core.yaml
internalTLS:
  enabled: true
  existingSecret: paladin-planes-tls
```

```yaml
# paladin-console.yaml
backend:
  tls: true
  caSecret:
    name: paladin-planes-tls
```

## Capabilities

Capability tokens — budgets, delegation, the console's capability and billing
pages — are off by default, because they need a key the chart should not make
for you: it signs credentials agents keep, so replacing it invalidates every
capability already issued. Generate one Ed25519 key and keep it:

```bash
openssl genpkey -algorithm ed25519 -out capability.pem
kubectl -n paladin create secret generic paladin-capability-signing-key --from-file=key.pem=capability.pem
```

```yaml
# paladin-core.yaml
capabilitySigningKeySecret:
  name: paladin-capability-signing-key
config:
  capability:
    enabled: true
    issuer_name: paladin-prod    # the `iss` claim; Paladin refuses to start without one
    trusted_issuers: [paladin-prod]
    signing_key_path: /etc/paladin-capability/key.pem
```

The chart mounts the Secret at `/etc/paladin-capability`, so
`signing_key_path` is that directory plus the Secret's key. Without a key
path Paladin generates a key at boot, and refuses to start that way outside
`local`, `dev`, `test` and `ci`: every restart would invalidate every token.

## GitOps

`lookup` cannot see the cluster when the chart is rendered client-side, as
ArgoCD does, so the generated signing key and admin password would change on
every sync. Provide both instead:

```yaml
auth:
  signingKey:
    existingSecret: paladin-auth-signing-key   # key: signing_key
bootstrap:
  admin:
    enabled: false   # ship the paladin-bootstrap-admin Secret yourself
```

The capability key is never generated by the chart, so under GitOps it is
provided the same way as outside it — see [Capabilities](#capabilities).
