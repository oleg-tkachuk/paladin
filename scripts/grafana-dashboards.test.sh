#!/usr/bin/env bash
# grafana-dashboards.test.sh — every dashboard query under deploy/grafana
# must parse.
#
# A panel with a PromQL syntax error shows "no data", which reads the same as
# a quiet system. promtool parses every dashboard query wrapped as a recording
# rule. The alerting rules live in the backend chart and are checked by
# chart-alerts.test.sh.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network, no Docker.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root/deploy/grafana"

command -v promtool >/dev/null 2>&1 || {
    echo "!!! promtool is not installed; the Grafana dashboard queries went unchecked" >&2
    exit 1
}

# Grafana variables are not PromQL. Parsing needs a literal range for
# $__rate_interval, and the "All" regex for a templated label filter.
readonly RATE_INTERVAL_STANDIN="5m"
readonly ALL_VALUES_STANDIN=".*"

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

# The refused-charges panel and the PaladinCapabilityChargesRefused alert must
# call the same outcomes refusals. A panel that also counts overrun (a charge
# that committed) or error disagrees with the alert without failing anywhere.
readonly ALERT_VALUES="$root/backend/deploy/chart/values.yaml"
readonly REFUSED_PANEL="Refused capability charges by tenant"
readonly REFUSED_SELECTOR_PATTERN='limes_outcome=~"([^"]*)"'

alert_outcomes=$(yq -r '.metrics.alerts.rules.capabilityChargesRefused.refusedOutcomes | join("|")' "$ALERT_VALUES")
panel_expr=$(python3 - "$REFUSED_PANEL" ./paladin-operations.json <<'PY'
import json, sys
title, path = sys.argv[1], sys.argv[2]
exprs = [t["expr"] for p in json.load(open(path))["panels"] if p["title"] == title for t in p["targets"]]
if len(exprs) != 1:
    sys.exit(f"!!! {path}: expected one query on panel {title!r}, found {len(exprs)}")
print(exprs[0])
PY
)
if [[ ! $panel_expr =~ $REFUSED_SELECTOR_PATTERN ]]; then
    echo "!!! panel \"$REFUSED_PANEL\" selects no limes_outcome=~ set: $panel_expr" >&2
    exit 1
fi
if [[ ${BASH_REMATCH[1]} != "$alert_outcomes" ]]; then
    echo "!!! panel \"$REFUSED_PANEL\" counts ${BASH_REMATCH[1]}; the alert's refusedOutcomes are $alert_outcomes" >&2
    exit 1
fi
