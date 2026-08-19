# Security policy

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

Report privately through GitHub:

1. Go to the [Security tab](https://github.com/oleg-tkachuk/paladin/security/advisories/new).
2. Open a draft advisory ("Report a vulnerability").

That channel is private until an advisory is published, and it gives us a
place to work on a fix with you before anything is disclosed.

If GitHub private reporting is unavailable to you for any reason, open a
public issue containing **only** the words "security report, please make
contact" and nothing else — no details, no reproduction — and a maintainer
will arrange a private channel.

### What to include

A report is actionable when it says what an attacker gains, not only what
looks wrong. Where you can:

- the component (`backend/internal/auth`, the `capability` module, the
  Next.js BFF, a Helm chart, …) and the version or commit;
- the principal you started as, and the authority you ended with;
- a reproduction — a request sequence, a config fragment, a failing test;
- anything you already know about the blast radius (one tenant? all of
  them? the platform plane?).

### What to expect

This is a pre-1.0 project maintained by a small number of people, so these
are honest intentions rather than a contractual SLA:

| Stage | Target |
| --- | --- |
| Acknowledgement of your report | 3 working days |
| Initial assessment (valid / not, rough severity) | 10 working days |
| Fix or documented mitigation for a confirmed high-severity issue | 30 days |

We will credit you in the advisory unless you ask us not to.

## Scope

The interesting attack surface, roughly in order of how much we care:

- **Authorisation** — anything that lets a principal act outside its
  tenant, escalate its role set, or bypass a Cedar policy decision.
- **Capability tokens** — forging, replaying past revocation, delegating
  wider than the parent, or spending past a budget ceiling.
- **Tenant isolation** — reading or writing another tenant's rows,
  objects, audit records or metrics. Postgres row-level security is a
  primary control here, so an RLS bypass is in scope even without a
  demonstrated data leak.
- **Authentication** — JWT validation, API-token verification, the OAuth
  authorisation server, the MCP resource server, and the BFF's session
  handling.
- **Object storage** — presigned URL scope, bucket routing, and anything
  that lets one tenant's key resolve to another tenant's bytes.
- **Supply chain** — the published container images and Helm charts.

## Not vulnerabilities

These are known, deliberate, and documented; reports about them will be
closed with a pointer here.

- **Credentials committed to this repository.** `backend/configs/*.yaml`,
  the `values-local.yaml` chart overlays and the e2e fixtures ship working
  development credentials on purpose, so a clone runs. They are public by
  design. The backend refuses to start with any of them outside an
  allow-listed disposable environment — see
  `backend/internal/config/weak_secrets.go`. A report that a default
  credential is publicly readable is not a finding; a report that the gate
  can be evaded is.
- **`oidc.disableAuth: true` in the local overlays.** Local-development
  parity, guarded the same way.
- **The admin console's `/config` page accepting a pasted token.** That is
  its purpose in a development build.
- Missing security headers, cookie flags or TLS settings on a stack
  brought up by `task e2e-up`. The compose stack is a development
  convenience and is not hardened; report these against the Helm charts,
  which are the deployment path we intend people to use.

## Security model

If you are looking for where to start, these are the load-bearing pieces:

- [`docs/adr/0010-capability-as-establishing-credential.md`](docs/adr/0010-capability-as-establishing-credential.md)
  — what a capability is allowed to establish on its own.
- [`docs/adr/0012-machine-principals-may-delete-their-own-objects.md`](docs/adr/0012-machine-principals-may-delete-their-own-objects.md)
  — the deliberate widening of machine-principal authority, and its limits.
- [`docs/adr/0008-mcp-oauth-resource-server.md`](docs/adr/0008-mcp-oauth-resource-server.md)
  and [`0009-oauth-authorization-server.md`](docs/adr/0009-oauth-authorization-server.md)
  — the token-issuing surfaces.
- [`capability/README.md`](capability/README.md) — the primitive itself,
  including its threat model.

## Supported versions

Pre-1.0: only `main` is supported. Fixes land there and are released from
there; there are no maintained release branches yet.
