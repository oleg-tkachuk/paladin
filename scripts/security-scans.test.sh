#!/usr/bin/env bash
# security-scans.test.sh — backend/tests/api/security/run-*.sh fail when their
# scanner did not run, and pass when it ran and reported findings.
#
# Each script used to end its scanner call with `|| true`, so a missing tool,
# a package gosec could not load or a Docker that would not start all printed
# "scan complete" and exited 0.
#
# The scanners are stubbed on PATH; SHIM_EXIT is their exit code and
# SHIM_REPORT what they write as the report (empty: no report).
#
# Runs from `task -t Taskfile.dev.yaml verify-all` — no Docker, no network.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
work="$(mktemp -d "${TMPDIR:-/tmp}/security-scans.XXXXXX")"
trap 'rm -rf "$work"' EXIT

readonly SCRIPTS_DIR=backend/tests/api/security
mkdir -p "$work/$SCRIPTS_DIR" "$work/bin"
cp "$root/$SCRIPTS_DIR"/run-*.sh "$work/$SCRIPTS_DIR/"

# One stub for every scanner: writes SHIM_REPORT to the path after the
# scanner's output flag, then exits SHIM_EXIT. docker gets the report name
# relative to the mounted reports directory, as ZAP does.
stub=$(cat <<'STUB'
#!/usr/bin/env bash
out=""
prev=""
for a in "$@"; do
    case "$prev" in
    -o | --output) out=$a ;;
    -r) out="$REPORTS/$a" ;;
    esac
    case "$a" in -out=*) out=${a#-out=} ;; esac
    prev=$a
done
if [ -n "$SHIM_REPORT" ] && [ -n "$out" ]; then printf "%s\n" "$SHIM_REPORT" >"$out"; fi
exit "$SHIM_EXIT"
STUB
)
for tool in go trivy nuclei docker; do
    printf '%s\n' "$stub" >"$work/bin/$tool"
    chmod +x "$work/bin/$tool"
done

failures=0
# name, script, want (pass|fail), stub exit, stub report
check() {
    local got=pass
    if ! REPORTS="$work/backend/reports/security" SHIM_EXIT=$4 SHIM_REPORT=$5 \
        PATH="$work/bin:$PATH" "$work/$SCRIPTS_DIR/$2" >/dev/null 2>&1; then
        got=fail
    fi
    if [ "$got" != "$3" ]; then
        printf 'FAIL %s: want %s, got %s\n' "$1" "$3" "$got"
        failures=$((failures + 1))
    fi
}

readonly GOSEC_CLEAN='{"Golang errors":{},"Issues":[]}'
readonly GOSEC_FINDINGS='{"Golang errors":{},"Issues":[{"rule_id":"G115"}]}'
readonly GOSEC_LOAD_ERROR='{"Golang errors":{"x.go":[{"error":"undefined: y"}]},"Issues":[]}'
readonly ANY_REPORT='{"Results":[]}'
# The shell's code for a command it cannot find, and `go tool`'s for a tool
# the module does not declare.
readonly EXIT_NOT_FOUND=127
readonly EXIT_GO_NO_TOOL=2

check "gosec, nothing found" run-gosec.sh pass 0 "$GOSEC_CLEAN"
check "gosec, findings" run-gosec.sh pass 1 "$GOSEC_FINDINGS"
check "gosec, a package it could not load" run-gosec.sh fail 1 "$GOSEC_LOAD_ERROR"
check "gosec, not a tool of the module" run-gosec.sh fail "$EXIT_GO_NO_TOOL" ""
check "gosec, exit 1 and no report" run-gosec.sh fail 1 ""
check "gosec, an unreadable report" run-gosec.sh fail 0 "not json"

check "trivy, scanned" run-trivy.sh pass 0 "$ANY_REPORT"
check "trivy, not installed" run-trivy.sh fail "$EXIT_NOT_FOUND" ""
check "trivy, failed" run-trivy.sh fail 1 ""

check "nuclei, scanned" run-nuclei.sh pass 0 "finding"
check "nuclei, not installed" run-nuclei.sh fail "$EXIT_NOT_FOUND" ""

check "zap, passed" run-zap.sh pass 0 "<html>"
check "zap, FAILs found" run-zap.sh pass 1 "<html>"
check "zap, WARNs only" run-zap.sh pass 2 "<html>"
check "zap, its own exception" run-zap.sh fail 3 ""
check "zap, docker could not run the image" run-zap.sh fail 125 ""
check "zap, docker not installed" run-zap.sh fail "$EXIT_NOT_FOUND" ""
check "zap, a finding code and no report" run-zap.sh fail 1 ""

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf '%s\n' "security scans: fail when the scanner did not run, pass on findings"
