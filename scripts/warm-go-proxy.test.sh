#!/usr/bin/env bash
# warm-go-proxy.test.sh — the proxy is asked for the module and version the
# tag names, against a local stand-in for the proxy.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
work=$(mktemp -d "${TMPDIR:-/tmp}/warm-proxy.XXXXXX")
pid=""
trap '[[ -n "$pid" ]] && kill "$pid" 2>/dev/null; rm -rf "$work"' EXIT

failed=0
fail() { printf 'FAIL %s\n' "$1"; failed=1; }

# The stand-in serves exactly the files a correct request names.
mkdir -p "$work/github.com/o/r/sdk/go/@v" "$work/github.com/o/r/capability/@v"
echo '{"Version":"v0.16.0"}' >"$work/github.com/o/r/sdk/go/@v/v0.16.0.info"
echo '{"Version":"v0.3.0"}' >"$work/github.com/o/r/capability/@v/v0.3.0.info"
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$work" >/dev/null 2>&1 &
pid=$!
for _ in $(seq 50); do curl -fs "http://127.0.0.1:$port/" >/dev/null 2>&1 && break; sleep 0.1; done

warm() { GITHUB_REPOSITORY=o/r GOPROXY_URL="http://127.0.0.1:$port" "$root/scripts/warm-go-proxy.sh" "$1"; }

[[ "$(warm sdk/go/v0.16.0)" == *'"v0.16.0"'* ]] || fail "the SDK tag asked for the wrong path"
[[ "$(warm capability/v0.3.0)" == *'"v0.3.0"'* ]] || fail "the capability tag asked for the wrong path"
for bad in v6.1.0 sdk/go/latest api/v0.12; do
    if warm "$bad" >/dev/null 2>&1; then fail "$bad was taken for a module tag"; fi
done

[[ $failed -eq 0 ]] || exit 1
echo "warm go proxy: each module tag asks the proxy for its own module and version"
