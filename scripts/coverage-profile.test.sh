#!/usr/bin/env bash
# coverage-profile.test.sh — scripts/coverage-profile.sh on a module of its own.
#
# Two packages' tests each reach one branch of a function in the first; with
# -coverpkg the merged profile lists each of its blocks once, both branches
# covered, where a plain -coverprofile lists them once per test package.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
readonly PROFILE="$root/scripts/coverage-profile.sh"
readonly EXIT_USAGE=2

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0
fail() { echo "coverage-profile: $1" >&2; failures=$((failures + 1)); }

mkdir -p "$work/a" "$work/b"
printf 'module example.com/m\n\ngo 1.27.0\n' > "$work/go.mod"
cat > "$work/a/a.go" <<'EOF'
package a

// Sign names the sign of n.
func Sign(n int) string {
	if n < 0 {
		return "negative"
	}
	return "not negative"
}
EOF
cat > "$work/a/a_test.go" <<'EOF'
package a

import "testing"

func TestNegative(t *testing.T) {
	if Sign(-1) != "negative" {
		t.Fatal("sign")
	}
}
EOF
cat > "$work/b/b_test.go" <<'EOF'
package b

import (
	"testing"

	"example.com/m/a"
)

func TestNotNegative(t *testing.T) {
	if a.Sign(1) != "not negative" {
		t.Fatal("sign")
	}
}
EOF

profile="$work/out/coverage.out"
(cd "$work" && "$PROFILE" "-coverprofile=$profile" -coverpkg=./... ./...) > /dev/null 2>&1 ||
    fail "the merge failed"

blocks=$(grep -c '^example.com/m/a/a.go:' "$profile" || true)
unique=$(grep '^example.com/m/a/a.go:' "$profile" | cut -d' ' -f1 | sort -u | wc -l | tr -d ' ')
uncovered=$(grep '^example.com/m/a/a.go:' "$profile" | awk '$3 == 0' | wc -l | tr -d ' ')
[ "$blocks" -gt 0 ] || fail "the profile has no block of a/a.go"
[ "$blocks" = "$unique" ] || fail "a/a.go has $blocks blocks listed, $unique distinct"
[ "$uncovered" = 0 ] || fail "$uncovered blocks of a/a.go read as uncovered, though each branch has a test"

got=0
"$PROFILE" "$work/no-flag" > /dev/null 2>&1 || got=$?
[ "$got" = "$EXIT_USAGE" ] || fail "a first argument without -coverprofile= exits $got, want $EXIT_USAGE"

if [ "$failures" -gt 0 ]; then
    exit 1
fi
echo "coverage-profile: each block once, covered by any package that reached it"
