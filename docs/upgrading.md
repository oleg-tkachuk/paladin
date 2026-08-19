# Upgrading

## v4.0.0 — the `paladin` rename

The project was renamed from `paladin` / `paladin` to `paladin`
on 2026-08-19. The rename went all the way down: it changed runtime
contracts, not only source identifiers.

**The supported upgrade path is to reprovision from scratch.** There is
no in-place migration, and none is planned.

This is deliberate. Pre-1.0 the project does not carry a
backward-compatibility promise (constitution, Principle IV), and the
rename touches Postgres roles, row-level-security policy internals,
credential formats, wire paths and the environment-variable prefix at
once. A migration that got any one of those half-right would leave a
cluster in a state harder to diagnose than a clean install — see
*Two silent failures* below for why.

If you are installing Paladin for the first time, none of this applies.
Follow [the README](../README.md); a fresh install is already correct.

### What reprovisioning means

You lose the control-plane database: tenants, buckets, ObjectKeys,
capabilities, API tokens, event subscriptions, and the audit log. You do
not lose object bytes — those live in the S3 backend and are untouched —
but with the `objects` rows gone they are orphaned, referenced by
nothing. Treat them as garbage to be collected, or re-import them
deliberately; Paladin will not adopt them on its own.

If that is not acceptable for your deployment, do not upgrade. Dump what
you need first, or stay on v3.

### Procedure

1. Take a dump of the old database if you want any of it for reference.
   Nothing in v4 reads it.
2. Reissue every API token. Old tokens carry the `paladin_pat_` prefix and
   are not recognised — see below. There is no dual-prefix grace period.
3. Rewrite every `PALADIN_*` environment variable to `PALADIN_*`. Do this
   before starting the new pods, not after.
4. Drop the old database and roles, or point the new deployment at a
   fresh one.
5. Deploy v4 and let `migrate` build the schema from empty.
6. Run `bootstrap` to create the platform admin.
7. Regenerate every client from the new protos.

### Two silent failures to watch for

Most of the rename fails loudly. Two do not, and both look like a
working system:

- **Stale RLS GUC.** Policies read a session-local GUC that moved from
  `paladin.tenant_id` to `paladin.tenant_id`. A v4 pod against v3 policies
  sets a GUC nothing reads, so every tenant-scoped query matches zero
  rows. No error, no log line — an empty console that looks like an
  empty deployment. This is the failure mode that makes a
  partially-applied migration worse than a clean install.
- **Ignored environment variables.** The config loader keys on the
  `PALADIN_` prefix. Anything still named `PALADIN_*` is not rejected, it is
  simply not seen, and the process starts on defaults — 57 variables
  can go quiet at once. Grep your manifests before deploying, not
  after.

### The complete list of breaking changes

| Contract | v3 | v4 |
| --- | --- | --- |
| Postgres roles | `paladin_app`, `paladin_migrate`, `paladin_reaper` | `paladin_app`, `paladin_migrate`, `paladin_reaper` |
| RLS session GUC | `paladin.tenant_id` | `paladin.tenant_id` |
| RLS helper function | `paladin_session_tenant_id()` | `paladin_session_tenant_id()` |
| API token prefix | `paladin_pat_…` | `paladin_pat_…` |
| Event type names | `paladin.object.uploaded`, `paladin.bucket.updated`, … | `paladin.*` |
| Proto package / RPC paths | `/paladin.data.v1.ObjectService/GetObject` | `/paladin.data.v1.ObjectService/GetObject` |
| HTTP headers | `X-PALADIN-*` | `X-Paladin-*` |
| Environment prefix | `PALADIN_` (57 variables) | `PALADIN_` |
| OAuth scopes | `paladin.read`, … | `paladin.*` |
| Default bucket names | `paladin-primary`, `paladin-archive`, `paladin-data` | `paladin-primary`, `paladin-archive`, `paladin-data` |
| Go module path | `github.com/oleg-tkachuk/paladin` | `github.com/oleg-tkachuk/paladin-private` |
| Chart / image names | `paladin`, `paladin-*` | `paladin-core`, `paladin-console` |

Two entries need a word of explanation.

**Default bucket names** are config defaults, not a rename applied to
your storage. Existing buckets keep the names they have. Either override
them in config or create the new ones — Paladin does not rename a bucket
under you.

**Event type names** break subscribers that filter on type. A consumer
matching `paladin.object.uploaded` goes quiet rather than erroring, in the
same way the environment variables do. Update filters as part of the
cutover, not afterwards.

### Deployments in a cluster

The Helm release name changed, so the old `paladin*` /
`paladin-*` Deployments, Services, ServiceAccounts, Certificates and Linkerd
Servers are pruned and `paladin-core-*` / `paladin-console` ones created in
their place. mTLS certificates regenerate (SANs `paladin-core-api` /
`paladin-core-admin`, SPIFFE `…/sa/paladin-core-api`). Expect a brief
in-namespace disruption, and confirm afterwards that the `/login`
redirect, the BFF's backend health aggregation and internal mTLS have all
recovered.
