#!/usr/bin/env bash
# admin-audience-roles.test.sh — the console's list of roles that reach the
# admin plane must be IAM's.
#
# IAM issues the paladin-admin audience to apiutil.AdminAudienceRoles; the
# console hides every admin view from anyone outside
# ADMIN_AUDIENCE_ROLES in frontend/src/constants/roles.ts. A role in one list
# and not the other either shows a view that can only be refused or hides one
# the role is meant to use.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly GO_FILE="$root/backend/internal/api/apiutil/roles.go"
readonly TS_FILE="$root/frontend/src/constants/roles.ts"

python3 - "$GO_FILE" "$TS_FILE" <<'PY'
import re, sys

go, ts = (open(p).read() for p in sys.argv[1:3])

go_consts = dict(re.findall(r'^\s*(Role\w+)\s*=\s*"([^"]+)"', go, re.M))
go_block = re.search(r'var AdminAudienceRoles = \[\]string\{(.*?)\}', go, re.S)
ts_consts = dict(re.findall(r'^\s*(\w+):\s*"([^"]+)"', ts, re.M))
ts_block = re.search(r'ADMIN_AUDIENCE_ROLES[^=]*=\s*\[(.*?)\]', ts, re.S)
if not go_block or not ts_block:
    sys.exit("!!! a list is missing — this check is asserting nothing")

go_roles = {go_consts[n] for n in re.findall(r'\b(Role\w+)\b', go_block.group(1))}
ts_roles = {ts_consts[n] for n in re.findall(r'ROLES\.(\w+)', ts_block.group(1))}
if not go_roles:
    sys.exit("!!! AdminAudienceRoles is empty — this check is asserting nothing")
if go_roles != ts_roles:
    sys.exit(f"!!! admin audience roles differ\n      backend only: {sorted(go_roles - ts_roles)}\n      console only: {sorted(ts_roles - go_roles)}")
print(f"admin audience: {len(go_roles)} roles, the same in IAM and the console")
PY
