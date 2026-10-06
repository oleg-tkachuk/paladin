#!/usr/bin/env python3
"""Check both charts' NetworkPolicies against the guarantees they claim.

`networkPolicies.enabled` was false in every values file for months, so these
templates were rendered by nothing, and a template error in them would have
reached the first cluster that turned them on. They are on by default now;
this gate renders every values file of both charts and requires a policy over
every workload, and keeps the role-level checks below.

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

# The gate enables them itself, together with every role, since a role that is
# off renders no policy and would otherwise leave a hole in what this checks.
ROLES = ["api", "admin", "worker", "dispatcher", "mcp", "ingest"]

# Observability as a cluster that pulls metrics runs it. The two values the
# policies derive ports from are set to something other than the chart's
# defaults, so a port that is hardcoded instead of derived shows up as a
# mismatch rather than passing by coincidence.
OBSERVED = {
    "config.otel.enabled": "true",
    "metrics.mode": "scrape",
    "metrics.port": "19095",
    "config.otel.endpoint": "collector.observability:14318",
    "networkPolicies.monitoring.namespace": "scrapers",
}
METRICS_PORT = int(OBSERVED["metrics.port"])
OTLP_PORT = int(OBSERVED["config.otel.endpoint"].rsplit(":", 1)[1])
MONITORING_NS = OBSERVED["networkPolicies.monitoring.namespace"]
# An API server port other than the chart's default, as OrbStack's is: every
# role reads its secrets through the API at boot, and a policy is matched on
# the endpoint port, so a port hardcoded in the template instead of read from
# networkPolicies.kubeAPIPorts leaves the pod crash-looping on its first read.
KUBE_API_PORT = 26443
KUBE_API = {"networkPolicies.kubeAPIPorts": f"{{{KUBE_API_PORT}}}"}
# The roles that serve /metrics, each on the metrics port alone (ADR-0023);
# mcp serves none.
SCRAPE_LISTENER_ROLES = ("api", "admin", "worker", "dispatcher", "ingest")


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


def egress_reaches_port(doc: dict, port: int) -> bool:
    """Whether a policy lets its pods out to anywhere on `port`."""
    for rule in doc["spec"].get("egress") or []:
        if not rule:  # `- {}` — open egress
            return True
        if not rule.get("to") and port in {p.get("port") for p in rule.get("ports") or []}:
            return True
    return False


def admits_role(doc: dict, role: str) -> bool:
    """Whether a policy admits pods of another role of this release."""
    for rule in doc["spec"].get("ingress") or []:
        for peer in rule.get("from") or []:
            labels = (peer.get("podSelector") or {}).get("matchLabels") or {}
            if labels.get("app.kubernetes.io/component") == role and "namespaceSelector" not in peer:
                return True
    return False


def ports_admitting(doc: dict, role: str) -> set[int]:
    """The ports a policy opens to pods of another role of this release."""
    ports: set[int] = set()
    for rule in doc["spec"].get("ingress") or []:
        for peer in rule.get("from") or []:
            labels = (peer.get("podSelector") or {}).get("matchLabels") or {}
            if labels.get("app.kubernetes.io/component") == role and "namespaceSelector" not in peer:
                ports |= {p["port"] for p in rule.get("ports") or []}
    return ports


def egress_reaches_role(doc: dict, role: str, ports: set[int]) -> bool:
    """Whether a policy lets its pods out to another role, on one of `ports`.

    The ingress half alone proves nothing: the admin pod's own default-deny
    egress refused its calls to the MCP bridge while the bridge's policy
    admitted admin, and the /mcp page showed the server as down."""
    for rule in doc["spec"].get("egress") or []:
        peers = rule.get("to")
        to_role = not peers or any(
            (peer.get("podSelector") or {}).get("matchLabels", {}).get("app.kubernetes.io/component") == role
            and "namespaceSelector" not in peer
            for peer in peers
        )
        rule_ports = {p["port"] for p in rule.get("ports") or []}
        if to_role and (not rule_ports or rule_ports & ports):
            return True
    return False


# Calls one role makes to another inside the release, as the chart's own
# config wires them: (caller, callee, what makes the call).
IN_RELEASE_CALLS = [
    ("admin", "worker", "SystemService.GetPlatformStats"),
    ("admin", "dispatcher", "SystemService.GetDispatcherStats"),
    ("admin", "mcp", "MCPInspectService status and sessions via mcp.http.sessions_url"),
    ("mcp", "api", "the MCP bridge"),
    ("mcp", "admin", "the MCP bridge"),
]


# Roles the console dials directly: /api/health/all reads each one's health
# snapshot. Refused, the console's health page shows the role as down.
UI_HEALTH_CALLS = ["api", "admin", "worker", "dispatcher", "mcp", "ingest"]
UI_LABELS = {"app.kubernetes.io/name": "paladin-console"}


def admits_ui(doc: dict) -> bool:
    for rule in doc["spec"].get("ingress") or []:
        for peer in rule.get("from") or []:
            labels = (peer.get("podSelector") or {}).get("matchLabels") or {}
            if "namespaceSelector" not in peer and all(labels.get(k) == v for k, v in UI_LABELS.items()) and labels:
                return True
    return False


def in_release_problems(policies: dict[str, dict]) -> list[str]:
    """A call the chart makes between its own roles must get through.

    Refused, each of these is a console page that renders empty rather than
    an error: RLS-style silence, at the network layer."""
    problems: list[str] = []
    for caller, callee, why in IN_RELEASE_CALLS:
        if callee not in policies or caller not in policies:
            continue
        if not admits_role(policies[callee], caller):
            problems.append(f"role {callee!r} does not admit {caller!r} ({why})")
        elif not egress_reaches_role(policies[caller], callee, ports_admitting(policies[callee], caller)):
            problems.append(f"role {caller!r} has no egress to {callee!r} ({why})")
    for role in UI_HEALTH_CALLS:
        if role in policies and not admits_ui(policies[role]):
            problems.append(f"role {role!r} does not admit the console (/api/health/all)")
    return problems


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


def kube_api_problems(chart: Path) -> list[str]:
    """Every role must reach the API server on the port it is told to.

    Not loud either: the pod crash-loops on "connection refused" from the
    API's ClusterIP, which reads as the API being down rather than refused."""
    problems: list[str] = []
    for doc in render(chart, KUBE_API):
        if doc.get("kind") != "NetworkPolicy" or not component(doc):
            continue
        if "Egress" in (doc["spec"].get("policyTypes") or []) and not egress_reaches_port(doc, KUBE_API_PORT):
            problems.append(
                f"role {component(doc)!r} cannot reach the Kubernetes API on port "
                f"{KUBE_API_PORT} (networkPolicies.kubeAPIPorts) — its secret read is refused")
    return problems


CHARTS = ("backend/deploy/chart", "frontend/deploy/chart")
CONSOLE_CHART = "frontend/deploy/chart"
BACKEND_CHART = "backend/deploy/chart"


def helm_docs(chart: Path, values: list[Path]) -> list[dict]:
    """Render `chart` with exactly these values files, as YAML documents."""
    cmd = ["helm", "template", "netpoltest", str(chart)]
    for v in values:
        cmd += ["-f", str(v)]
    out = subprocess.run(cmd, capture_output=True, text=True)
    if out.returncode != 0:
        print(f"!!! {chart} does not render with {[v.name for v in values]}", file=sys.stderr)
        print("      " + out.stderr.strip().replace("\n", "\n      "), file=sys.stderr)
        sys.exit(1)
    asjson = subprocess.run(["yq", "ea", "-o=json", "-I=0", "[.]", "-"],
                            input=out.stdout, capture_output=True, text=True, check=True)
    return [d for d in json.loads(asjson.stdout) if d]


def pod_labels(doc: dict) -> dict:
    return ((doc.get("spec") or {}).get("template") or {}).get("metadata", {}).get("labels") or {}


def selects(policy: dict, labels: dict) -> bool:
    want = (policy["spec"].get("podSelector") or {}).get("matchLabels") or {}
    return all(labels.get(k) == v for k, v in want.items())


def every_workload_problems(root: Path) -> list[str]:
    """Under the values we ship, every Deployment sits under a NetworkPolicy.

    Policies are on by default, so a values file that renders a workload with
    none has either turned them off or added a workload no policy names."""
    problems = []
    for rel in CHARTS:
        chart = root / rel
        required = chart / REQUIRED_VALUES
        for values in sorted(chart.glob("values*.yaml")):
            files = [chart / "values.yaml"] + ([required] if required.exists() else [])
            if values.name != "values.yaml":
                files.append(values)
            docs = helm_docs(chart, files)
            policies = [d for d in docs if d.get("kind") == "NetworkPolicy"]
            for d in docs:
                if d.get("kind") == "Deployment" and not any(selects(p, pod_labels(d)) for p in policies):
                    problems.append(f"{rel} {values.name}: Deployment {d['metadata']['name']} "
                                    f"is under no NetworkPolicy")
    return problems


def console_problems(root: Path) -> list[str]:
    """The console's own policy, and the label contract with the backend's.

    The backend admits the console by networkPolicies.ui.podLabels; the console
    chart decides its pod labels. If the two drift, every RPC from the console
    is dropped at the backend's default-deny, with nothing saying why."""
    problems = []
    docs = helm_docs(root / CONSOLE_CHART, [root / CONSOLE_CHART / "values.yaml"])
    deployments = [d for d in docs if d.get("kind") == "Deployment"]
    policies = [d for d in docs if d.get("kind") == "NetworkPolicy"]
    if len(deployments) != 1 or len(policies) != 1:
        return [f"console: expected one Deployment and one NetworkPolicy, "
                f"rendered {len(deployments)} and {len(policies)}"]
    pod, policy = pod_labels(deployments[0]), policies[0]
    if not selects(policy, pod):
        problems.append("console: its NetworkPolicy does not select its own pods")
    if set(policy["spec"].get("policyTypes") or []) != {"Ingress", "Egress"}:
        problems.append("console: its policy does not cover both directions")
    if not egress_has_dns(policy):
        problems.append("console: no DNS egress — it cannot resolve the planes")
    backend_release = subprocess.run(
        ["yq", ".backend.release", str(root / CONSOLE_CHART / "values.yaml")],
        capture_output=True, text=True, check=True).stdout.strip()
    reaches_backend = any(
        ((to.get("podSelector") or {}).get("matchLabels") or {}).get("app.kubernetes.io/instance") == backend_release
        for rule in policy["spec"].get("egress") or [] for to in rule.get("to") or [])
    if not reaches_backend:
        problems.append(f"console: no egress to the backend release {backend_release!r}")
    port = deployments[0]["spec"]["template"]["spec"]["containers"][0]["ports"][0]["containerPort"]
    if not any(port in {p.get("port") for p in rule.get("ports") or []}
               for rule in policy["spec"].get("ingress") or []):
        problems.append(f"console: nothing may reach its port {port}")
    # Only the ingress controller: the backend trusts the console's forwarded
    # client address, and the console passes on whatever X-Forwarded-For it
    # receives, so any other peer could name its own address.
    controller = json.loads(subprocess.run(
        ["yq", "-o=json", ".networkPolicies.ingressController.podLabels",
         str(root / CONSOLE_CHART / "values.yaml")],
        capture_output=True, text=True, check=True).stdout)
    for rule in policy["spec"].get("ingress") or []:
        peers = rule.get("from") or []
        if not peers or any(((peer.get("podSelector") or {}).get("matchLabels") or {}) != controller
                            or "ipBlock" in peer for peer in peers):
            problems.append(f"console: admits {peers or 'every peer'}, not only the ingress "
                            f"controller {controller} — a client could name its own address")
    ui_labels = json.loads(subprocess.run(
        ["yq", "-o=json", ".networkPolicies.ui.podLabels", str(root / BACKEND_CHART / "values.yaml")],
        capture_output=True, text=True, check=True).stdout)
    if any(pod.get(k) != v for k, v in ui_labels.items()):
        problems.append(f"backend admits the console as {ui_labels}, but its pods carry {pod}")
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
    problems += kube_api_problems(root / "backend/deploy/chart")
    problems += in_release_problems({component(p): p for p in policies if component(p)})
    problems += console_problems(root)
    problems += every_workload_problems(root)

    if problems:
        print("!!! the rendered NetworkPolicies break what they promise", file=sys.stderr)
        for problem in problems:
            print(f"      {problem}", file=sys.stderr)
        return 1
    print(f"backend: {len(policies)} NetworkPolicies render; "
          f"{len(covered)} roles covered, baseline denies both directions; "
          f"console covered; every shipped values file puts each Deployment under a policy")
    return 0


if __name__ == "__main__":
    sys.exit(main())
