#!/usr/bin/env bash
# py-sdk-compat.test.sh — the Python SDK's declared ranges, its generated
# stubs and its compatibility matrix must agree.
#
# - Every stub demands a protobuf runtime at least its gencode version, so the
#   declared floor must be that version: lower, and pip installs a runtime the
#   stubs refuse at import; higher, and the range locks out runtimes that work.
# - The matrix (sdk/python/compat.json) must test that floor, each protobuf
#   major the range admits, and every Python from requires-python up.
#
# Fast: reads files, installs nothing. The matrix itself runs in CI and in
# `scripts/py-sdk-compat.sh --matrix`.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
readonly SDK="$root/sdk/python"
# The library that defines the requirement-specifier format; parsed with it,
# not with a regular expression.
# renovate: datasource=pypi depName=packaging
readonly PACKAGING_VERSION=26.3

uv run --quiet --no-project --with "packaging==${PACKAGING_VERSION}" python - "$SDK" <<'PY'
import json
import pathlib
import re
import subprocess
import sys

from packaging.requirements import Requirement
from packaging.specifiers import SpecifierSet
from packaging.version import Version

sdk = pathlib.Path(sys.argv[1])
project = json.loads(
    subprocess.run(
        ["yq", "-p", "toml", "-o", "json", ".project", str(sdk / "pyproject.toml")],
        check=True, capture_output=True, text=True,
    ).stdout
)
matrix = json.loads((sdk / "compat.json").read_text())
failures = []

deps = {r.name: r for r in map(Requirement, project["dependencies"])}
protobuf = deps["protobuf"].specifier
floors = [Version(s.version) for s in protobuf if s.operator == ">="]
if len(floors) != 1:
    sys.exit(f"protobuf needs exactly one >= floor, got {protobuf}")
floor = floors[0]

# The gencode stamp every stub carries: "# Protobuf Python Version: X.Y.Z".
stamp = re.compile(r"^# Protobuf Python Version: (\S+)$", re.M)
gencode = {
    Version(m.group(1))
    for f in (sdk / "src").rglob("*_pb2.py")
    for m in [stamp.search(f.read_text())]
    if m
}
if gencode != {floor}:
    failures.append(f"stubs are gencode {sorted(map(str, gencode))}, declared floor is {floor}")

cells = [Requirement(w) for w in matrix["with"]]
tested = {Version(next(iter(r.specifier)).version) for r in cells if r.name == "protobuf"}
if floor not in tested:
    failures.append(f"the matrix does not test the protobuf floor {floor}")
ceilings = [Version(s.version) for s in protobuf if s.operator == "<"]
if len(ceilings) != 1:
    sys.exit(f"protobuf needs exactly one < ceiling, got {protobuf}")
# Each major with a release the range accepts.
admitted = {
    m
    for m in range(floor.major, ceilings[0].major + 1)
    if protobuf.contains(max(floor, Version(f"{m}.0")))
}
missing = admitted - {v.major for v in tested}
if missing:
    failures.append(f"the matrix tests no protobuf {sorted(missing)}.x the range admits")
for v in tested:
    if v not in protobuf:
        failures.append(f"the matrix tests protobuf {v}, outside the declared {protobuf}")

python_floor = Version(next(iter(SpecifierSet(project["requires-python"]))).version)
pythons = sorted(Version(p) for p in matrix["python"])
if not pythons or pythons[0] != python_floor:
    failures.append(f"the matrix starts at Python {pythons[0] if pythons else '-'}, requires-python at {python_floor}")
for a, b in zip(pythons, pythons[1:]):
    if b.minor != a.minor + 1:
        failures.append(f"the matrix skips Python between {a} and {b}")

if failures:
    print("!!! the Python SDK's ranges, stubs and matrix disagree:", file=sys.stderr)
    for f in failures:
        print(f"      {f}", file=sys.stderr)
    sys.exit(1)
print(f"python sdk: gencode {floor} = declared floor; matrix covers {len(pythons)} Pythons and protobuf {sorted(map(str, tested))}")
PY
