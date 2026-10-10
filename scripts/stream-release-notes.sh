#!/usr/bin/env bash
# stream-release-notes.sh — release notes for an SDK tag.
#
# Usage: stream-release-notes.sh <tag>      e.g. sdk/go/v0.12.0
#
# Prints the stream's title on the first line and Markdown notes after it:
# the feat, fix, perf, security and revert commits since the stream's
# previous tag that touch the stream's own files, and a compare link. A
# breaking commit — `!` after the type, or a BREAKING CHANGE footer — is
# listed first, under Behaviour changes, with a link to its section of
# docs/upgrading.md: pre-1.0 it is only a minor, so the notes are where it
# shows.
# GitHub's generated notes list every pull request between two tags, most of
# them the product's; these list only what the stream released. The stream,
# its tag format and its paths come from .github/release/release.config.cjs, the file the
# release workflow cuts the tag with.

set -euo pipefail

tag="${1:?usage: stream-release-notes.sh <tag>}"
root="${RELEASE_ROOT:-$(git rev-parse --show-toplevel)}"
repo="${GITHUB_REPOSITORY:-oleg-tkachuk/paladin}"
cd "$root"

# Released commit types, as the stream's bump counts them; the rest
# (docs, ci, chore, …) release nothing and are left out.
readonly FEATURES='^feat(\([^)]*\))?!?: '
readonly FIXES='^(fix|perf|security|revert)(\([^)]*\))?!?: '
readonly BANG='^[a-z]+(\([^)]*\))?!: '
readonly FOOTER='^BREAKING[ -]CHANGE: '
# The upgrade guide, whose "## <tags> — …" section naming this tag a breaking
# release links to. The full tag, because a product version is "v…" too.
readonly UPGRADING=docs/upgrading.md
readonly DEFAULT_BRANCH=main

# "<stream> <title> <tag prefix> <path>…" for the stream whose tag this is.
# shellcheck disable=SC2016 # JavaScript, with its own ${version}
meta=$(node -e '
  const { streams } = require(process.argv[1] + "/.github/release/release.config.cjs");
  const titles = { sdk: "SDK" };
  for (const [name, s] of Object.entries(streams)) {
    const prefix = s.tagFormat.replace("${version}", "");
    if (name in titles && process.argv[2].startsWith(prefix)) {
      console.log(name, titles[name], prefix, (s.analyzer.include || []).join(" "));
      process.exit(0);
    }
  }
  process.exit(1);
' "$root" "$tag") || {
    echo "!!! $tag is not an SDK tag" >&2
    exit 1
}
read -r _ title prefix paths <<<"$meta"
read -r -a include <<<"$paths"

version="${tag#"$prefix"}"
previous=$(git describe --tags --abbrev=0 --match "${prefix}*" "${tag}^" 2>/dev/null || true)

bullets() { while IFS= read -r line; do printf -- '- %s\n' "$line"; done <<<"$1"; }

printf '%s %s\n' "$title" "$version"
if [[ -z "$previous" ]]; then
    printf '\nThe first release of this stream.\n'
    exit 0
fi

log="" breaking=""
while IFS= read -r hash; do
    [[ -n "$hash" ]] || continue
    line=$(git log -1 --format='%s (%h)' "$hash")
    if grep -qE "$BANG" <<<"$line" || git log -1 --format=%B "$hash" | grep -qE "$FOOTER"; then
        breaking+="$line"$'\n'
    else
        log+="$line"$'\n'
    fi
done < <(git log --no-merges --format=%H "${previous}..${tag}" -- "${include[@]}")
breaking=${breaking%$'\n'} log=${log%$'\n'}
features=$(grep -E "$FEATURES" <<<"$log" || true)
fixes=$(grep -E "$FIXES" <<<"$log" || true)

# GitHub's anchor for a heading: lower case, punctuation dropped, spaces to
# hyphens.
anchor() { LC_ALL=C tr '[:upper:]' '[:lower:]' | LC_ALL=C sed -e 's/[^a-z0-9 _-]//g' -e 's/ /-/g'; }

if [[ -n "$breaking" ]]; then
    printf '\n## Behaviour changes\n\n'
    bullets "$breaking"
    heading=$(grep -m1 -E "^## (.*[ ,])?${tag//./\\.}([ ,]|$)" "$UPGRADING" 2>/dev/null || true)
    if [[ -n "$heading" ]]; then
        # main, not the tag: the heading is named after the tag is cut
        # (.github/release/name-upgrade-sections.mjs), so the tag's own copy
        # still says Unreleased and has no such anchor.
        printf '\nMigrating: [%s](https://github.com/%s/blob/%s/%s#%s)\n' \
            "${heading#\#\# }" "$repo" "$DEFAULT_BRANCH" "$UPGRADING" "$(anchor <<<"${heading#\#\# }")"
    else
        printf '\nMigrating: [%s](https://github.com/%s/blob/%s/%s)\n' "$UPGRADING" "$repo" "$tag" "$UPGRADING"
    fi
fi

if [[ -n "$features" ]]; then
    printf '\n## Features\n\n'
    bullets "$features"
fi
if [[ -n "$fixes" ]]; then
    printf '\n## Fixes\n\n'
    bullets "$fixes"
fi
if [[ -z "$breaking" && -z "$features" && -z "$fixes" ]]; then
    printf '\nNo feature or fix commits touched %s in this release.\n' "${include[*]}"
fi
printf '\n**Full changes:** https://github.com/%s/compare/%s...%s\n' "$repo" "$previous" "$tag"
