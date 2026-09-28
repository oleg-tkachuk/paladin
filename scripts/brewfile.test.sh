#!/usr/bin/env bash
# brewfile.test.sh — every tool this repo probes for must be in the Brewfile,
# or named there as a deliberate exclusion.
#
# A Brewfile rots the moment a script starts shelling out to something new: the
# tool announces itself by failing a precondition on somebody else's machine,
# which is the situation the Brewfile was written to end. So the list is checked
# against the probes rather than maintained by memory.
#
# Both halves have to be deliberate. `# not-brew: <tool>` in the Brewfile is how
# a tool says "something else pins me" — go.mod, corepack, a container image —
# and each one there carries the reason. An exclusion is a decision, not a way
# to silence this check.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network, no brew
# invocation.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

[[ -f Brewfile ]] || { echo "!!! no Brewfile at the repo root" >&2; exit 1; }

# Tools the repo asks for by name: shell preconditions and the Python checkers.
# `|| true` on each grep, and not for tidiness: under `set -e` a grep that
# matches nothing exits 1 and kills the subshell on the spot, so a renamed
# pattern would abort this script with no output at all — failing, but saying
# nothing. The empty-set guard below is what should report that.
probed=$(
    {
        grep -rhoE 'command -v "?[a-z0-9_.-]+' \
            scripts/ backend/scripts/ frontend/scripts/ \
            Taskfile.yaml backend/Taskfile.yaml frontend/Taskfile.yaml \
            backend/tasks/ lefthook.yml 2>/dev/null | sed -E 's/command -v "?//' || true
        grep -rhoE 'shutil\.which\("[a-z0-9_-]+' scripts/ 2>/dev/null | sed 's/shutil.which("//' || true
        grep -rhoE '^[[:space:]]+"[a-z0-9_-]+": "https' scripts/*.py 2>/dev/null |
            sed -E 's/^[[:space:]]+"([a-z0-9_-]+)".*/\1/' || true
    } | sort -u
)

# What the Brewfile provides. A formula whose binary differs from its name says
# so in a trailing comment — `brew "kubernetes-cli"   # kubectl`.
provided=$(
    sed -nE 's/^brew "([^"]+)".*(# bin: ([a-z0-9_-]+)).*$/\1 \3/p; s/^brew "([^"]+)".*$/\1/p' Brewfile |
        awk '{ print ($2 == "" ? $1 : $2) }' | sort -u
)
excluded=$(sed -nE 's/^# not-brew: ([a-z0-9_-]+).*/\1/p' Brewfile | sort -u)

# Guard against a silent no-op: a rename on either side would otherwise empty a
# set and leave the comparison below trivially true.
for set_name in probed provided excluded; do
    if [[ -z "${!set_name}" ]]; then
        echo "!!! the $set_name set is empty — this check is asserting nothing" >&2
        exit 1
    fi
done

missing=$(comm -23 <(printf '%s\n' "$probed") <(printf '%s\n' "$provided" "$excluded" | sort -u))
if [[ -n "$missing" ]]; then
    {
        echo "!!! the Brewfile does not account for tools this repo probes for:"
        printf '%s\n' "$missing" | sed 's/^/      /'
        echo '      Add `brew "<formula>"`, or `# not-brew: <tool>` with the reason it is pinned elsewhere.'
    } >&2
    exit 1
fi

echo "Brewfile: $(printf '%s\n' "$provided" | wc -l | tr -d ' ') formulae, \
$(printf '%s\n' "$excluded" | wc -l | tr -d ' ') documented exclusions, \
$(printf '%s\n' "$probed" | wc -l | tr -d ' ') probed tools accounted for"
