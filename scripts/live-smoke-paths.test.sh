#!/usr/bin/env bash
# live-smoke-paths.test.sh — the console paths live-smoke.sh calls must be
# the ones the console itself calls.
#
# live-smoke.sh drives the console's session role over HTTP, so it spells its
# paths in shell; the console spells them in frontend/src/constants/index.ts.
# The smoke went on calling the Next.js routes after the console moved behind
# the session role, and failed only when someone next ran it against a
# cluster. This fails at the commit that moves one without the other.
#
# Runs from `task -t Taskfile.dev.yaml verify-repo` — no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly SMOKE="$root/scripts/live-smoke.sh"
readonly CONSTANTS="$root/frontend/src/constants/index.ts"

uv run --no-project python - "$SMOKE" "$CONSTANTS" <<'PY'
import re, sys

smoke, ts = (open(p).read() for p in sys.argv[1:3])

def shell(name):
    m = re.search(rf'^readonly {name}=(\S+)$', smoke, re.M)
    if not m:
        sys.exit(f"!!! live-smoke.sh declares no {name} — this check is asserting nothing")
    return m.group(1)

prefix = re.search(r'export const SESSION_PREFIX = "([^"]+)"', ts)
paths = re.search(r'export const SESSION_PATHS = \{(.*?)\n\} as const', ts, re.S)
rpc = re.search(r'export const SESSION_RPC_PREFIX = `\$\{SESSION_PREFIX\}([^`]*)`', ts)
if not (prefix and paths and rpc):
    sys.exit("!!! the console's session constants are not where this check reads them")
console = {k: prefix.group(1) + v for k, v in re.findall(r'(\w+): `\$\{SESSION_PREFIX\}([^`]*)`', paths.group(1))}

want = {
    "SESSION_LOGIN_PATH": console.get("login"),
    "SESSION_LOGOUT_PATH": console.get("logout"),
    "SESSION_RPC_PREFIX": prefix.group(1) + rpc.group(1),
}
failed = [f"{name}={shell(name)}, the console's is {path}" for name, path in want.items() if shell(name) != path]
if failed:
    sys.exit("!!! live-smoke.sh calls other console paths than the console:\n      " + "\n      ".join(failed))
print(f"live smoke: {len(want)} console paths, the same as the console's")
PY
