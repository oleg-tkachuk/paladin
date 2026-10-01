#!/usr/bin/env python3
"""Check the backend chart's NetworkPolicies against the guarantees they claim.

`networkPolicies.enabled` is false in every values file we ship — it needs an
enforcing CNI — so these 266 lines of template were rendered by nothing: not
`helm lint`, not the render gate, not chart-values.test.sh. A template error in
them would have reached the first cluster that turned them on.

Rendering is the cheap half. The half worth asserting is the one that fails
catastrophically and silently: the baseline policy denies both directions for
every pod of the release, so a role with no policy of its own is not "less
protected", it is **cut off entirely** — no DNS, no Postgres, no ingress. Add a
role tomorrow, turn policies on, and that pod is dead with nothing in the chart
saying why. So the role set is derived from the rendered Deployments and
compared, rather than listed here where it would go stale.

    python3 scripts/chart-netpol.py
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from pathlib import Path

# The inputs a chart cannot default; a render without them fails before
# any NetworkPolicy is looked at.
REQUIRED_VALUES = "ci/required-values.yaml"

# Off by default and not enabled by any values file, so the gate sets them
# itself — together with every role, since a role that is off renders no policy
# and would otherwise leave a hole in what this checks.
ROLES = ["api", "admin", "worker", "dispatcher", "mcp", "ingest"]

# Observability as a cluster that pulls metrics runs it. The two values the
# policies derive ports from are set to something other than the chart's
# defaults, so a port that is hardcoded instead of derived shows up as a
# mismatch rather than passing by coincidence.
OBSERVED = {
    "config.otel.enabled": "true",
    "config.otel.metrics_exporter": "prometheus",
    "config.otel.metrics_addr": "0.0.0.0:19095",
    "config.otel.endpoint": "collector.observability:14318",
    "networkPolicies.monitoring.namespace": "scrapers",
}
METRICS_PORT = int(OBSERVED["config.otel.metrics_addr"].rsplit(":", 1)[1])
OTLP_PORT = int(OBSERVED["config.otel.endpoint"].rsplit(":", 1)[1])
MONITORING_NS = OBSERVED["networkPolicies.monitoring.namespace"]
# The roles whose scrape listener is a port of its own (config.otel.metrics_addr);
# the others serve /metrics on an ops port their policy already opens.
SCRAPE_LISTENER_ROLES = ("api", "admin")


def render(chart: Path, extra: dict[str, str] | None = None) -> list[dict]:
    # yq, not jq: helm emits YAML and jq parses only JSON, so `jq .` on a
    # rendered chart fails at the first `key: value`. yq is the only new tool
    # this gate needs.
    where = {"helm": "https://helm.sh/docs/intro/install/",
             "yq": "https://github.com/mikefarah/yq#install"}
    for tool, url in where.items():
        if not shutil.which(tool):
            print(f"!!! {tool} is not installed; the NetworkPolicies went unchecked — {url}",
                  file=sys.stderr)
            sys.exit(1)
    cmd = ["helm", "template", "netpoltest", str(chart), "--set", "networkPolicies.enabled=true"]
    required = chart / REQUIRED_VALUES
    if required.exists():
        cmd += ["-f", str(required)]
    for role in ROLES:
        cmd += ["--set", f"deployments.{role}.enabled=true"]
    for key, value in (extra or {}).items():
        cmd += ["--set-string" if key.endswith(("addr", "endpoint")) else "--set", f"{key}={value}"]
    out = subprocess.run(cmd, capture_output=True, text=True)
    if out.returncode != 0:
        print("!!! the chart does not render with networkPolicies.enabled=true", file=sys.stderr)
        print("      " + out.stderr.strip().replace("\n", "\n      "), file=sys.stderr)
        sys.exit(1)
    asjson = subprocess.run(["yq", "ea", "-o=json", "-I=0", "[.]", "-"],
                            input=out.stdout, capture_output=True, text=True, check=True)
    return [d for d in json.loads(asjson.stdout) if d]


def component(doc: dict) -> str | None:
    """The role a document is about.

    A NetworkPolicy names it in `podSelector` and a Deployment in `selector`.
    A Job names it in NEITHER: Kubernetes generates a Job's selector from a
    controller UID, so reading `selector` alone returned None for both Jobs
    this chart ships and dropped them out of every comparison below.

    The pod template is the honest source in all three cases, because pod
    labels are what a NetworkPolicy actually matches against."""
    spec = doc.get("spec") or {}
    for labels in (
        (spec.get("podSelector") or {}).get("matchLabels"),
        (spec.get("selector") or {}).get("matchLabels"),
        ((spec.get("template") or {}).get("metadata") or {}).get("labels"),
    ):
        if labels and "app.kubernetes.io/component" in labels:
            return labels["app.kubernetes.io/component"]
    return None


def egress_has_dns(doc: dict) -> bool:
    for rule in doc["spec"].get("egress") or []:
        if not rule:  # `- {}` — allow all, DNS included
            return True
        ports = {p.get("port") for p in rule.get("ports") or []}
        if 53 in ports:
            return True
    return False


def admits_scrape(doc: dict) -> bool:
    """Whether a policy lets the monitoring namespace reach the metrics port."""
    for rule in doc["spec"].get("ingress") or []:
        ports = {p.get("port") for p in rule.get("ports") or []}
        if METRICS_PORT not in ports:
            continue
        for peer in rule.get("from") or []:
            labels = (peer.get("namespaceSelector") or {}).get("matchLabels") or {}
            if labels.get("kubernetes.io/metadata.name") == MONITORING_NS:
                return True
    return False


def egress_reaches_otlp(doc: dict) -> bool:
    for rule in doc["spec"].get("egress") or []:
        if not rule:  # `- {}` — open egress
            return True
        if OTLP_PORT in {p.get("port") for p in rule.get("ports") or []}:
            return True
    return False


def observability_problems(chart: Path) -> list[str]:
    """With metrics pulled and traces pushed, both must still get through.

    Neither failure is loud. A scrape the policy drops is a target that reads
    down; a span it drops is a trace that never arrives."""
    problems: list[str] = []
    policies = {component(d): d for d in render(chart, OBSERVED)
                if d.get("kind") == "NetworkPolicy" and component(d)}
    for role in SCRAPE_LISTENER_ROLES:
        if role in policies and not admits_scrape(policies[role]):
            problems.append(
                f"role {role!r} does not admit {MONITORING_NS!r} on its metrics port "
                f"{METRICS_PORT} — the scrape is dropped and the target reads down")
    for role, doc in sorted(policies.items()):
        if "Egress" in (doc["spec"].get("policyTypes") or []) and not egress_reaches_otlp(doc):
            problems.append(
                f"role {role!r} cannot reach the OTLP collector on port {OTLP_PORT} "
                f"— its spans are dropped")
    return problems


def main() -> int:
    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"],
                               capture_output=True, text=True, check=True).stdout.strip())
    docs = render(root / "backend/deploy/chart")

    policies = [d for d in docs if d.get("kind") == "NetworkPolicy"]
    # Jobs as well as Deployments, and the omission was not theoretical: the
    # baseline selects on the chart's name+instance labels, which the migrate
    # and bootstrap Job pods carry, while the per-role policies select on
    # `component` — which for them is `migrate` / `bootstrap` and matched none.
    # Both Jobs sat under default-deny with no allow of their own, and this
    # check, written to catch exactly that, compared only half the workloads.
    deployed = {component(d) for d in docs
                if d.get("kind") in ("Deployment", "Job")} - {None}
    problems: list[str] = []

    if not policies:
        problems.append("no NetworkPolicy rendered at all, with networkPolicies.enabled=true")
    if not deployed:
        problems.append("no Deployment or Job rendered — the role comparison below checks nothing")

    baseline = [p for p in policies if p["metadata"]["name"].endswith("-default-deny")]
    if len(baseline) != 1:
        problems.append(f"expected exactly one default-deny baseline, found {len(baseline)}")
    else:
        types = set(baseline[0]["spec"].get("policyTypes") or [])
        if types != {"Ingress", "Egress"}:
            problems.append(f"the baseline denies {sorted(types) or 'nothing'}, not both directions")
        for direction in ("ingress", "egress"):
            if baseline[0]["spec"].get(direction):
                problems.append(f"the baseline carries {direction} rules; it must allow nothing")

    covered = {component(p) for p in policies} - {None}
    for role in sorted(deployed - covered):
        problems.append(
            f"role {role!r} has a workload but no NetworkPolicy — the baseline denies "
            f"both directions, so this pod would have no DNS, no database and no ingress"
        )
    for role in sorted(covered - deployed):
        problems.append(f"a NetworkPolicy selects {role!r}, which this chart does not deploy")

    for p in policies:
        role = component(p)
        if role is None:
            continue
        if not egress_has_dns(p):
            problems.append(f"role {role!r} has no DNS egress — it cannot resolve any name")

    problems += observability_problems(root / "backend/deploy/chart")

    if problems:
        print("!!! backend: the rendered NetworkPolicies break what they promise", file=sys.stderr)
        for problem in problems:
            print(f"      {problem}", file=sys.stderr)
        return 1
    print(f"backend: {len(policies)} NetworkPolicies render; "
          f"{len(covered)} roles covered, baseline denies both directions")
    return 0


if __name__ == "__main__":
    sys.exit(main())
