#!/usr/bin/env bash
# no-private-refs.test.sh — a public repository may not point at a private one,
# and may not carry anybody's home directory.
#
# Two properties, both asserted positively. Neither one names a private
# repository, because this file is published: a guard holding a list of the
# names it forbids publishes exactly what it exists to keep out.
#
#   1. Every `<owner>/<repo>` reference under this account resolves to a repo a
#      reader can actually open. The allowlist below is public names only, so a
#      reference to any private sibling fails without this file ever spelling
#      one. Go module paths, chart and image repositories, remote Taskfile
#      includes and documentation links all take this form, which is what makes
#      the check worth its cost — those are the references that break a reader.
#
#   2. No absolute home path. This catches the leaks nobody thinks to look for,
#      because they do not arrive through prose: a linter report, a coverage
#      file, a generated manifest or a tool's cached diagnostic carries the
#      path of the machine it ran on, and committing one publishes a username
#      and a directory layout.
#
# What this deliberately does NOT check is a private name written as prose in a
# comment — "the service that owns 8080", named. That cannot be gated from
# inside a public repository without listing the names, so it is a review
# question, not a gate. Say what a thing is, not which of our repositories does
# it, and the question does not come up.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no cluster.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

# The account this repository belongs to. References to any OTHER account are
# third-party dependencies and none of this check's business.
readonly OWNER="oleg-tkachuk"

# Public repositories under OWNER that this tree may legitimately name.
#   paladin                    this repository — Go module path, docs links
#   paladin-core               its backend container image, ghcr.io/OWNER/…
#   paladin-console            its frontend container image
#   charts                     its Helm charts, oci://ghcr.io/OWNER/charts/…
#   taskfiles                 the shared Task library, a remote include
readonly ALLOWED_REPOS=(paladin paladin-core paladin-console charts taskfiles)

# Lockfiles carry base64 integrity hashes and vendored dependency graphs; the
# owner names in them are npm's and Go's business, not a reference this tree
# wrote. Excluded from the owner check, still read by the home-path one.
readonly LOCKFILES=(':!*pnpm-lock.yaml' ':!*package-lock.json' ':!go.sum' ':!*buf.lock')

if [[ ${#ALLOWED_REPOS[@]} -eq 0 ]]; then
    echo "!!! no repositories allowlisted — this check would reject everything" >&2
    exit 1
fi

# ── 1. every OWNER/<repo> reference is a public one ─────────────────────────
#
# `git grep -o` over tracked files only: an untracked scratch file is not
# published and is not this check's business.
# A while-read loop rather than `mapfile`: macOS ships bash 3.2, where mapfile
# does not exist, and this gate runs on a developer's machine before it runs
# anywhere else.
seen=()
while IFS= read -r repo; do
    [[ -n $repo ]] && seen+=("$repo")
done < <(
    git grep -h -o -E "${OWNER}/[A-Za-z0-9._-]+" -- . "${LOCKFILES[@]}" 2>/dev/null |
        sed -E "s#^${OWNER}/##; s#\.git\$##" | sort -u
)

# The repository names itself in its own go.mod and README; zero matches means
# the pattern stopped matching, not that the tree became clean.
if [[ ${#seen[@]} -eq 0 ]]; then
    echo "!!! no ${OWNER}/… references found at all — this check is asserting nothing" >&2
    echo "      go.mod alone should carry one. The pattern has stopped matching." >&2
    exit 1
fi

failed=0
for repo in "${seen[@]}"; do
    allowed=0
    for ok in "${ALLOWED_REPOS[@]}"; do
        [[ $repo == "$ok" ]] && allowed=1 && break
    done
    if [[ $allowed -eq 0 ]]; then
        failed=1
        {
            echo "!!! the tree references ${OWNER}/${repo}, which is not an allowlisted public repository:"
            git grep -n -E "${OWNER}/${repo}([^A-Za-z0-9._-]|\$)" -- . "${LOCKFILES[@]}" | sed 's/^/      /'
            echo "      Either it is public and belongs in ALLOWED_REPOS in this file,"
            echo "      or a reader cannot open it and the reference has to go."
        } >&2
    fi
done

# ── 2. no absolute home paths ───────────────────────────────────────────────
#
# Anchored on the separator so a word like "Username" cannot match, and on the
# three roots a home directory actually has on macOS and Linux.
if hits=$(git grep -I -n -E '(/Users/|/home/[a-z_][a-z0-9_-]*/|/root/)' -- . ':!scripts/no-private-refs.test.sh' 2>/dev/null); then
    failed=1
    {
        echo "!!! a tracked file carries an absolute home path:"
        printf '%s\n' "$hits" | sed 's/^/      /'
        echo "      Make it relative to the repository root, or to \$HOME. A"
        echo "      committed home path publishes a username and a layout."
    } >&2
fi

[[ $failed -ne 0 ]] && exit 1

echo "private refs: ${#seen[@]} ${OWNER}/… references, all public; no home paths"
