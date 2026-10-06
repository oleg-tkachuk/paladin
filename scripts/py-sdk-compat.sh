#!/usr/bin/env bash
# py-sdk-compat.sh — install the Python SDK's wheel into a fresh environment
# beside the given requirements, and run its tests there.
#
#   scripts/py-sdk-compat.sh [--without-tls] <python> [requirement...]
#   scripts/py-sdk-compat.sh 3.10 protobuf==6.33.5
#   scripts/py-sdk-compat.sh 3.14 hatchet-sdk==1.41.1
#   scripts/py-sdk-compat.sh --matrix     every cell of sdk/python/compat.json
#
# The wheel is installed with its tls extra, which the TLS tests need, unless
# --without-tls: that cell checks the SDK imports and works without it.
#
# The SDK's own lock resolves one protobuf; a consumer's environment resolves
# another, from the SDK's declared ranges and its own pins. This is that
# environment: the built wheel, not the source tree, so a range that admits a
# runtime the stubs refuse fails here rather than at a consumer's import.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly SDK="$root/sdk/python"
# The cells CI runs as a matrix; one list, so a local run checks the same ones.
readonly MATRIX="$SDK/compat.json"

if (($# < 1)); then
    echo "usage: $0 [--without-tls] <python> [requirement...] | $0 --matrix" >&2
    exit 2
fi

if [[ $1 == --matrix ]]; then
    jq -r '(.python[] as $p | .with[] as $w | [$p, $w, true]), (.include[] | [.python, .with, .tls != false]) | @tsv' "$MATRIX" |
        while IFS=$'\t' read -r python with tls; do
            echo "=== python $python, $with, tls extra $tls"
            if [[ $tls == true ]]; then
                "$0" "$python" "$with" </dev/null
            else
                "$0" --without-tls "$python" "$with" </dev/null
            fi
        done
    exit 0
fi

# The package's own name, for installing the wheel with an extra.
readonly PACKAGE=paladin-sdk
readonly TLS_EXTRA=tls
with_tls=true
if [[ $1 == --without-tls ]]; then
    with_tls=false
    shift
fi
python=$1
shift

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

uv build --quiet --wheel --out-dir "$work/dist" "$SDK"
wheel=$(find "$work/dist" -name '*.whl' | head -1)

# What the tests themselves import, at the versions the SDK's lock holds.
# Not the whole dev group: its generator pins protobuf, and its optional
# google-crc32c would hide the path where that extra is absent. biscuit-python
# is installed only where it has a wheel (before 3.14); the 3.14 cells run the
# path where the biscuit extra is absent.
readonly TEST_DEPS='^(pytest|cryptography|biscuit-python)=='
mapfile -t test_deps < <(cd "$SDK" && uv export --quiet --frozen --only-group dev --no-hashes --no-annotate --no-emit-project |
    grep -E "$TEST_DEPS")

uv venv --quiet --python "$python" "$work/venv"
sdk_requirement=$wheel
if [[ $with_tls == true ]]; then
    sdk_requirement="$PACKAGE[$TLS_EXTRA] @ file://$wheel"
fi
uv pip install --quiet --python "$work/venv" "$sdk_requirement" "${test_deps[@]}" "$@"

# From outside the source tree: the tests import the installed wheel.
cd "$SDK/tests"
"$work/venv/bin/python" -c '
import sys, google.protobuf, connectrpc, paladin
assert "site-packages" in paladin.__file__, paladin.__file__
import importlib.util
has_tls = importlib.util.find_spec("httpcore") is not None
assert has_tls == (sys.argv[1] == "true"), f"httpcore installed: {has_tls}, wanted: {sys.argv[1]}"
print(f"python {sys.version.split()[0]} protobuf {google.protobuf.__version__} tls {has_tls} paladin {paladin.__file__}")' "$with_tls"
"$work/venv/bin/python" -m pytest --quiet --rootdir "$SDK" -p no:cacheprovider .
