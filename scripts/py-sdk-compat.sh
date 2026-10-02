#!/usr/bin/env bash
# py-sdk-compat.sh — install the Python SDK's wheel into a fresh environment
# beside the given requirements, and run its tests there.
#
#   scripts/py-sdk-compat.sh <python> [requirement...]
#   scripts/py-sdk-compat.sh 3.10 protobuf==6.33.5
#   scripts/py-sdk-compat.sh 3.14 hatchet-sdk==1.41.1
#   scripts/py-sdk-compat.sh --matrix     every cell of sdk/python/compat.json
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
    echo "usage: $0 <python> [requirement...] | $0 --matrix" >&2
    exit 2
fi

if [[ $1 == --matrix ]]; then
    jq -r '(.python[] as $p | .with[] as $w | [$p, $w]), (.include[] | [.python, .with]) | @tsv' "$MATRIX" |
        while IFS=$'\t' read -r python with; do
            echo "=== python $python, $with"
            "$0" "$python" "$with" </dev/null
        done
    exit 0
fi

python=$1
shift

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

uv build --quiet --wheel --out-dir "$work/dist" "$SDK"
wheel=$(find "$work/dist" -name '*.whl' | head -1)

# The test runner at the version the SDK's lock holds.
pytest=$(cd "$SDK" && uv export --quiet --frozen --only-group dev --no-hashes --no-annotate | grep '^pytest==')

uv venv --quiet --python "$python" "$work/venv"
uv pip install --quiet --python "$work/venv" "$wheel" "$pytest" "$@"

# From outside the source tree: the tests import the installed wheel.
cd "$SDK/tests"
"$work/venv/bin/python" -c '
import sys, google.protobuf, connectrpc, paladin
assert "site-packages" in paladin.__file__, paladin.__file__
print(f"python {sys.version.split()[0]} protobuf {google.protobuf.__version__} paladin {paladin.__file__}")'
"$work/venv/bin/python" -m pytest --quiet --rootdir "$SDK" -p no:cacheprovider .
