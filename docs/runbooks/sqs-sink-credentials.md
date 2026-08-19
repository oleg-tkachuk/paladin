# Runbook: SQS event-sink credentials (on- and off-AWS)

How the dispatcher authenticates to Amazon SQS when delivering an event
subscription whose sink is `sqs`, and how to wire credentials when Paladin runs
**outside** EKS (or off AWS entirely), where there is no IRSA to lean on.

## How the sink resolves credentials

The SQS sink builds one `aws-sdk-go-v2` client per `(region, role_arn)` via
`awsconfig.LoadDefaultConfig` — see
[`internal/worker/sink_sqs.go`](../../backend/internal/worker/sink_sqs.go)
(`SQSClientPool.newClient`). That walks the **standard AWS credential chain**,
in order:

1. Environment — `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`
   (+ optional `AWS_SESSION_TOKEN`), `AWS_REGION`.
2. Shared config / credentials files (`~/.aws/*`, `AWS_PROFILE`).
3. Web-identity token (IRSA / EKS Pod Identity / any OIDC provider):
   `AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE`.
4. EC2 / ECS instance-metadata (IMDS).

The dispatcher only ever calls `LoadDefaultConfig` — it never reads a static
key from the sink config or the Paladin database. Credentials are a **deployment**
concern, injected into the dispatcher pod's environment; a sink config carries
only `queue_url`, `region`, and the optional cross-account `role_arn`.

`role_arn`, when set on the sink, is `sts:AssumeRole`'d **off** whatever the
chain above resolved (cached ~1h). So the chain still has to yield a usable
identity first — `role_arn` layers cross-account on top, it does not replace
the base credentials.

## On EKS (the common case) — nothing to wire in Paladin

Annotate the **dispatcher** ServiceAccount with the IAM role and let IRSA (or
EKS Pod Identity) inject the web-identity token. Chain step 3 picks it up; the
sink needs no `role_arn` unless the target queue lives in another account.

Grant the role, at minimum, on the target queue ARN:

```json
{ "Effect": "Allow",
  "Action": ["sqs:SendMessage", "sqs:SendMessageBatch"],
  "Resource": "arn:aws:sqs:<region>:<account>:<queue>" }
```

Cross-account: give the base role `sts:AssumeRole` on the target role, put the
target role ARN in the sink's `role_arn`, and let the target role hold the
`sqs:SendMessage*` grant.

## Off-AWS / non-EKS — static keys via a Secret

With no IRSA and no IMDS, chain steps 3–4 are dead. Provision a dedicated IAM
user scoped to `sqs:SendMessage` + `sqs:SendMessageBatch` on the queue, and
feed its access key to the dispatcher as **environment from a Kubernetes
Secret** — never inline the key into values or the sink config.

1. Create the Secret out-of-band (sealed-secrets / external-secrets / a manual
   `kubectl create secret` in a locked-down namespace). Keys must match the
   SDK's env-var names:

   ```
   kubectl -n paladin create secret generic paladin-dispatcher-aws \
     --from-literal=AWS_ACCESS_KEY_ID=AKIA... \
     --from-literal=AWS_SECRET_ACCESS_KEY=... \
     --from-literal=AWS_REGION=us-east-1
   ```

2. Reference it from the dispatcher deployment via the chart's `extraEnvFrom`
   hook (see `defaults.extraEnvFrom` in
   [`values.yaml`](../../backend/deploy/chart/values.yaml)):

   ```yaml
   deployments:
     dispatcher:
       extraEnvFrom:
         - secretRef:
             name: paladin-dispatcher-aws
   ```

   `extraEnvFrom` (and the sibling `extraEnv`, for one-off `valueFrom` refs)
   are appended after the chart's built-in env, so `envFrom` overrides nothing
   Paladin sets itself. The hook is per-role — only the dispatcher pod, the only
   one that talks to customer sinks, gets the credentials.

Because the whole material lives in a Secret, rotation is a Secret update plus
a `kubectl rollout restart deploy/paladin-core-dispatcher` — the SDK
re-reads the env on process start. (The sink's own TTL cache is for `k8s:`
credential **refs inside sink config**, a different path; static AWS env vars
are read once at client build.)

## Notes

- **Egress is already open.** The dispatcher NetworkPolicy allows all egress by
  design (it delivers to arbitrary customer endpoints) — see
  [`templates/networkpolicy.yaml`](../../backend/deploy/chart/templates/networkpolicy.yaml).
  SQS/STS on 443 need no extra rule.
- **Prefer federated identity over static keys.** Static IAM-user keys are the
  last resort: no automatic rotation, and a leaked key is valid until revoked.
  If the off-AWS platform can mint OIDC tokens, wire step 3 (`AWS_ROLE_ARN` +
  `AWS_WEB_IDENTITY_TOKEN_FILE`) via `extraEnv` + a projected-token volume
  instead.
- **Least privilege.** The user/role needs only `sqs:SendMessage` and
  `sqs:SendMessageBatch` on the specific queue ARN. FIFO queues need nothing
  extra — the sink sets `MessageGroupId`/`MessageDeduplicationId` itself.
