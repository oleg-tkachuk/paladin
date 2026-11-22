#!/usr/bin/env bash
# go-fuzz.test.sh — scripts/go-fuzz.sh on a module of its own.
#
# One package holds a target that passes and one that crashes on any
# non-empty input; another holds no target. The run must reach both targets,
# go on past the crash, write the crashing input under testdata/fuzz, and exit
# non-zero. A module with no target at all is refused rather than reported
# clean, and so is a missing argument.
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no network.

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
readonly FUZZ="$root/scripts/go-fuzz.sh"
readonly EXIT_USAGE=2
# A count rather than a duration: enough inputs for the mutator to leave the
# empty one, and the same on a slow runner as on a fast one.
readonly FUZZ_TIME=200x

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0
fail() { echo "go-fuzz: $1" >&2; failures=$((failures + 1)); }

mkdir -p "$work/m/a" "$work/m/b" "$work/empty/c"
printf 'module example.com/m\n\ngo 1.27.0\n' > "$work/m/go.mod"
printf 'module example.com/empty\n\ngo 1.27.0\n' > "$work/empty/go.mod"
cat > "$work/m/a/a_test.go" <<'EOF'
package a

import "testing"

func FuzzPasses(f *testing.F) {
	f.Add("seed")
	f.Fuzz(func(t *testing.T, s string) {})
}

func FuzzCrashes(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		if s != "" {
			t.Fatal("crash")
		}
	})
}
EOF
cat > "$work/m/b/b_test.go" <<'EOF'
package b

import "testing"

func TestNothing(t *testing.T) {}
EOF
cat > "$work/empty/c/c_test.go" <<'EOF'
package c

import "testing"

func TestNothing(t *testing.T) {}
EOF

out="$work/out.txt"
got=0
(cd "$work" && "$FUZZ" "$FUZZ_TIME" m) > "$out" 2>&1 || got=$?
[ "$got" = 1 ] || fail "a run with a crashing target exits $got, want 1"
grep -q 'go-fuzz: m ./a FuzzPasses' "$out" || fail "FuzzPasses did not run"
grep -q 'go-fuzz: m ./a FuzzCrashes' "$out" || fail "FuzzCrashes did not run"
grep -q '1 failed of 2 targets run' "$out" || fail "the summary does not name one failure of two targets"
grep -q 'm ./b' "$out" && fail "the package without a target was fuzzed"
crashers=$(find "$work/m/a/testdata/fuzz/FuzzCrashes" -type f 2>/dev/null | wc -l | tr -d ' ')
[ "$crashers" -gt 0 ] || fail "no crashing input was written under testdata/fuzz/FuzzCrashes"

got=0
(cd "$work" && "$FUZZ" "$FUZZ_TIME" empty) > /dev/null 2>&1 || got=$?
[ "$got" = 1 ] || fail "a module with no target exits $got, want 1"

got=0
"$FUZZ" "$FUZZ_TIME" > /dev/null 2>&1 || got=$?
[ "$got" = "$EXIT_USAGE" ] || fail "no module dir exits $got, want $EXIT_USAGE"

if [ "$failures" -gt 0 ]; then
    cat "$out" >&2
    exit 1
fi
echo "go-fuzz: every target found and run, a crash reported with its input"
