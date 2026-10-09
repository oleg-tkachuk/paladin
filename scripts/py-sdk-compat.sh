#!/usr/bin/env bash
# py-sdk-compat.sh — install the Python SDK's wheel into a fresh environment
# beside the given requirements, and run its tests there.
#
#   scripts/py-sdk-compat.sh [--without-tls] <python> [requirement...]
#   scripts/py-sdk-compat.sh 3.13 protobuf==6.33.5
#   scripts/py-sdk-compat.sh 3.14 hatchet-sdk==1.41.1
#   scripts/py-sdk-compat.sh --matrix     every cell of sdk/python/compat.json
#   scripts/py-sdk-compat.sh --list       those cells, one per line
#
# The wheel is installed with its tls extra, which the TLS tests need, unless
# --without-tls: that cell checks the SDK imports and works without it.
#
# The SDK's own lock resolves one protobuf; a consumer's environment resolves
# another, from the SDK's declared ranges and its own pins. This is that
# environment: the built wheel, not the source tree, so a range that admits a
# runtime the stubs refuse fails here rather than at a consumer's import.
#
# --matrix builds the wheel and reads the test dependencies once, installs
# every Python the cells need in one go, then runs the cells side by side,
# PY_SDK_COMPAT_JOBS at a time (default: one per CPU). Each cell's output is
# printed whole when it ends, folded into a group under GitHub Actions.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly SDK="$root/sdk/python"
# The cells CI runs; one list, so a local run checks the same ones.
readonly MATRIX="$SDK/compat.json"
# The package's own name, for installing the wheel with an extra.
readonly PACKAGE=paladin-sdk
readonly TLS_EXTRA=tls
# What the tests themselves import, at the versions the SDK's lock holds.
# Not the whole dev group: its generator pins protobuf, and its optional
# google-crc32c would hide the path where that extra is absent. biscuit-python
# is installed only where it has a wheel (before 3.14); the 3.14 cells run the
# path where the biscuit extra is absent.
readonly TEST_DEPS='^(pytest|cryptography|biscuit-python|protovalidate|protobuf-py|protobuf-py-ext)=='

usage() {
    echo "usage: $0 [--without-tls] <python> [requirement...] | $0 --matrix | $0 --list" >&2
    exit 2
}

# cells prints every cell as "python<TAB>requirement<TAB>tls", tls being true
# or false: each python × with pair with the extra, then each include entry
# as a cell of its own.
cells() {
    jq -r '(.python[] as $p | .with[] as $w | [$p, $w, true]), (.include[] | [.python, .with, .without_tls != true]) | @tsv' "$MATRIX"
}

# build_wheel builds the SDK's wheel into $1 and prints its path.
build_wheel() {
    uv build --quiet --wheel --out-dir "$1" "$SDK"
    find "$1" -name '*.whl' | head -1
}

# test_deps prints the test requirements, one per line.
test_deps() {
    (cd "$SDK" && uv export --quiet --frozen --only-group dev --no-hashes --no-annotate --no-emit-project) |
        grep -E "$TEST_DEPS"
}

# run_cell <wheel> <deps file> <tls> <python> [requirement...] installs the
# wheel and the test requirements into a fresh venv and runs the tests.
run_cell() {
    local wheel=$1 deps_file=$2 with_tls=$3 python=$4
    shift 4
    local venv
    venv=$(mktemp -d)
    # shellcheck disable=SC2064 # expand now: the variable is local
    trap "rm -rf '$venv'" RETURN

    local -a deps
    mapfile -t deps <"$deps_file"
    local sdk_requirement=$wheel
    if [[ $with_tls == true ]]; then
        sdk_requirement="${PACKAGE}[${TLS_EXTRA}] @ file://${wheel}"
    fi
    uv venv --quiet --python "$python" "$venv"
    uv pip install --quiet --python "$venv" "$sdk_requirement" "${deps[@]}" "$@"

    # From outside the source tree: the tests import the installed wheel.
    (
        cd "$SDK/tests"
        "$venv/bin/python" -c '
import sys, google.protobuf, connectrpc, paladin
assert "site-packages" in paladin.__file__, paladin.__file__
import importlib.util
has_tls = importlib.util.find_spec("httpcore") is not None
assert has_tls == (sys.argv[1] == "true"), f"httpcore installed: {has_tls}, wanted: {sys.argv[1]}"
print(f"python {sys.version.split()[0]} protobuf {google.protobuf.__version__} tls {has_tls} paladin {paladin.__file__}")' "$with_tls"
        "$venv/bin/python" -m pytest --quiet --rootdir "$SDK" -p no:cacheprovider .
    )
}

cpus() {
    getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1
}

# label names a cell the way CI names it.
label() {
    local text="python $1, $2"
    [[ $3 == true ]] || text+=", no tls extra"
    echo "$text"
}

run_matrix() {
    local work
    work=$(mktemp -d)
    # shellcheck disable=SC2064 # expand now: the variable is local
    trap "rm -rf '$work'" EXIT

    local wheel
    wheel=$(build_wheel "$work/dist")
    test_deps >"$work/deps"

    local -a rows
    mapfile -t rows < <(cells)
    # Every interpreter first, in one call: cells that share one would
    # otherwise race to download it.
    local -a pythons
    mapfile -t pythons < <(printf '%s\n' "${rows[@]}" | cut -f1 | sort -u)
    uv python install --quiet "${pythons[@]}"

    local jobs=${PY_SDK_COMPAT_JOBS:-$(cpus)}
    local i running=0
    for i in "${!rows[@]}"; do
        local python with tls
        IFS=$'\t' read -r python with tls <<<"${rows[$i]}"
        (
            if run_cell "$wheel" "$work/deps" "$tls" "$python" "$with" >"$work/$i.log" 2>&1 </dev/null; then
                echo ok >"$work/$i.status"
            else
                echo failed >"$work/$i.status"
            fi
        ) &
        running=$((running + 1))
        if ((running >= jobs)); then
            wait -n || true
            running=$((running - 1))
        fi
    done
    wait

    local -a failed=()
    for i in "${!rows[@]}"; do
        local python with tls name status
        IFS=$'\t' read -r python with tls <<<"${rows[$i]}"
        name=$(label "$python" "$with" "$tls")
        status=$(cat "$work/$i.status" 2>/dev/null || echo failed)
        if [[ ${GITHUB_ACTIONS:-} == true ]]; then
            echo "::group::$name — $status"
        else
            echo "=== $name — $status"
        fi
        cat "$work/$i.log"
        [[ ${GITHUB_ACTIONS:-} == true ]] && echo "::endgroup::"
        [[ $status == ok ]] || failed+=("$name")
    done

    if ((${#failed[@]} > 0)); then
        echo "!!! ${#failed[@]} of ${#rows[@]} cells failed:" >&2
        printf '      %s\n' "${failed[@]}" >&2
        exit 1
    fi
    echo "python sdk: all ${#rows[@]} cells passed"
}

(($# >= 1)) || usage
case $1 in
--list)
    cells
    exit 0
    ;;
--matrix)
    run_matrix
    exit 0
    ;;
esac

with_tls=true
if [[ $1 == --without-tls ]]; then
    with_tls=false
    shift
fi
(($# >= 1)) || usage
python=$1
shift

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
wheel=$(build_wheel "$work/dist")
test_deps >"$work/deps"
run_cell "$wheel" "$work/deps" "$with_tls" "$python" "$@"
