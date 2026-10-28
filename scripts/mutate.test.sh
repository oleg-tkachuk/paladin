#!/usr/bin/env bash
# mutate.test.sh — scripts/mutate.sh on a module of its own.
#
# A mutant the tests cannot tell apart survives and fails the run; a test
# that tells it apart kills it; and the mutated file is left as it was, with
# no mutate4go manifest in it.
#
# Runs from `task -t Taskfile.dev.yaml verify-all`; the first run fetches the
# tools in tools/go.mod.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly MUTATE="$root/scripts/mutate.sh"
readonly EXIT_SURVIVED=1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

mkdir -p "$work/sign"
printf 'module example.com/sign\n\ngo 1.27.0\n' > "$work/go.mod"
cat > "$work/sign/sign.go" <<'EOF'
package sign

// Positive reports whether n is above zero.
func Positive(n int) bool {
	return n > 0
}
EOF
original=$(cat "$work/sign/sign.go")

run() { # want-exit test-body
    local want=$1 got=0
    printf 'package sign\n\nimport "testing"\n\nfunc TestPositive(t *testing.T) {\n%s\n}\n' "$2" > "$work/sign/sign_test.go"
    (cd "$work" && "$MUTATE" sign/sign.go) > /dev/null 2>&1 || got=$?
    if [ "$got" != "$want" ]; then
        printf 'mutate: exit %s, want %s\n' "$got" "$want" >&2
        failures=$((failures + 1))
    fi
    if [ "$(cat "$work/sign/sign.go")" != "$original" ]; then
        echo "mutate: the mutated file was left changed" >&2
        failures=$((failures + 1))
    fi
}

# Without zero, > and >= read the same: the mutant survives.
run "$EXIT_SURVIVED" '	if !Positive(5) || Positive(-5) { t.Fatal("sign") }'
# Zero and one tell them apart from > 0: every mutant is killed.
run 0 '	if !Positive(5) || Positive(-5) || Positive(0) || !Positive(1) { t.Fatal("sign") }'

if [ "$failures" -gt 0 ]; then
    exit 1
fi
echo "mutate: a surviving mutant fails the run, a killed one passes, the file is left as it was"
