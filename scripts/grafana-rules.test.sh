#!/usr/bin/env bash
# grafana-rules.test.sh — the alert rules and dashboard queries under
# deploy/grafana must parse, and the alerts must fire when they should.
#
# A rule or panel with a PromQL syntax error is loaded by nobody and reported
# by nothing: the alert never fires and the panel shows "no data", which reads
# the same as a quiet system. promtool parses every rule file, runs the unit
# tests beside them, and parses every dashboard query wrapped as a recording
# rule.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network, no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root/deploy/grafana"

command -v promtool >/dev/null 2>&1 || {
    echo "!!! promtool is not installed; the Grafana rules and queries went unchecked" >&2
    exit 1
}

readonly RULE_FILES=(*-alerts.yaml)
readonly RULE_TESTS=(*-alerts.test.yaml)
# Grafana variables are not PromQL. Parsing needs a literal range for
# $__rate_interval, and the "All" regex for a templated label filter.
readonly RATE_INTERVAL_STANDIN="5m"
readonly ALL_VALUES_STANDIN=".*"

promtool check rules "${RULE_FILES[@]}"
promtool test rules "${RULE_TESTS[@]}"

queries=$(mktemp -t dashboard-queries.XXXXXX.yaml)
trap 'rm -f "$queries"' EXIT

python3 - "$RATE_INTERVAL_STANDIN" "$ALL_VALUES_STANDIN" "$queries" ./*.json <<'PY'
import json, re, sys
interval, all_values, out, dashboards = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
rules = []
for path in dashboards:
    for panel in json.load(open(path))["panels"]:
        for target in panel.get("targets", []):
            expr = target["expr"].replace("$__rate_interval", interval)
            expr = re.sub(r"\$[a-z_]+", all_values, expr)
            rules.append({"record": f"dashboard_query_{len(rules)}", "expr": expr})
if not rules:
    sys.exit(f"!!! no queries found in {dashboards}")
json.dump({"groups": [{"name": "dashboard-queries", "rules": rules}]}, open(out, "w"))
PY

promtool check rules "$queries"
