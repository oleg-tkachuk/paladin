# Runbook: resource-level questions cannot be answered on the OrbStack cluster

**Short version:** there are no `container_*` series in VictoriaMetrics on the
dev cluster. Any panel, alert or ad-hoc query built on `container_cpu_*`,
`container_memory_*` or `container_*_throttled_*` renders empty here while
looking correctly configured. This is OrbStack's kubelet, not our
configuration, and it does not reproduce on a normal cluster.

## How it presents

The scrape looks healthy. Alloy reports

```
up{job="cadvisor"} = 1
```

because the endpoint answers 200 — it answers with `machine_*` and
nothing else. So the failure mode is not a red target you would notice; it is
a green target serving nine lines where thousands are expected.

Confirm it in one command:

```bash
kubectl get --raw "/api/v1/nodes/orbstack/proxy/metrics/cadvisor" | grep -c "^container_"
```

`0` means you are looking at this problem. On a real kubelet the same command
returns thousands.

## What this costs

Questions of the form "was the api pod CPU-starved when that TLS handshake
timed out?" cannot be answered on this cluster. That is exactly the question
that first surfaced this — see the `container_*` metrics entry under
"Tooling and observability" in [BACKLOG.md](../../BACKLOG.md).

What still works, and is usually enough:

- Paladin's own application metrics (`paladin_*`) are unaffected — they come
  from the pods' own `/metrics`, not from the kubelet.
- `kubectl describe node` shows allocated requests/limits, though not usage.
- `kubectl top` needs metrics-server, which is not installed on this cluster
  either.

## Fixing it, if it becomes worth fixing

Two sources would work: cAdvisor as its own DaemonSet, or metrics-server
behind the `metrics.k8s.io` API (which gives `kubectl top` but not Prometheus
series, so it does not fill the panels).

Neither belongs in this repository. The Alloy install, the VictoriaMetrics
stack and the namespaces they live in are shared cluster infrastructure that
Paladin does not own and does not deploy — Paladin's charts stop at its own
namespace. Adding a DaemonSet to make Paladin's dashboards work would be
changing someone else's cluster to fix our view of it. Raise it with whoever
owns the observability stack instead.

## What NOT to conclude

An empty `container_cpu_usage_seconds_total` graph on this cluster is not
evidence that a pod used no CPU. It is evidence of nothing at all.
