#!/usr/bin/env bash
# generate.sh — regenerate the Python stubs from the contract in proto/.
#
# protoc comes from grpcio-tools, pinned in pyproject.toml, so the generated
# code does not depend on whichever protoc a machine has. buf only exports the
# contract together with its dependencies, which protoc cannot fetch itself.
#
# Only this contract and buf.validate are generated: google.* ships with
# protobuf and googleapis-common-protos.

set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
cd "$here"

readonly CONTRACT=../../proto
readonly OUT=src
readonly GENERATED=("$OUT/paladin/admin" "$OUT/paladin/common" "$OUT/paladin/data" "$OUT/paladin/iam" "$OUT/buf")

deps=$(mktemp -d)
trap 'rm -rf "$deps"' EXIT

buf export "$CONTRACT" --output "$deps"

rm -rf "${GENERATED[@]}"
files=()
while IFS= read -r f; do files+=("$f"); done < <(cd "$deps" && find paladin buf/validate -name '*.proto' | sort)

# The plugin is found on PATH; `uv run` puts the project's venv there.
uv run python -m grpc_tools.protoc \
    --proto_path="$deps" \
    --python_out="$OUT" \
    --pyi_out="$OUT" \
    --connect-python_out="$OUT" \
    "${files[@]}"

# The facade's per-plane classes come from the services just generated.
uv run python scripts/gen_facade.py

echo "python sdk: generated $(find "${GENERATED[@]}" -name '*.py' | wc -l | tr -d ' ') modules"
