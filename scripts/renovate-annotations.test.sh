#!/usr/bin/env bash
# renovate-annotations.test.sh — every `# renovate: datasource=… depName=…`
# comment in the repository must be one Renovate reads.
#
# The comment does nothing by itself: a custom regex manager in renovate.json
# has to cover the file and match the pinned line below it. The workflows
# carried such comments above every pinned tool while renovate.json had no
# manager for them, so none of those versions moved. This fails on an
# annotation no custom manager would extract, naming it.
#
# The task library's pin is the one version held the other way round: the
# library's own Renovate preset finds `?ref=` in every Taskfile with no
# annotation, so an annotation above it would have two managers propose the
# same change. This fails when renovate.json stops extending that preset, and
# on an annotation naming the library.
#
# renovate.json's patterns are JavaScript regular expressions; their named
# groups are translated to Python's spelling, the only difference they use.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

uv run --quiet --no-project python - <<'PY'
import json
import re
import subprocess
import sys

RENOVATE_CONFIG = "renovate.json"
# An annotation is a comment line of its own; renovate.json and the prose
# that describe it mention the text mid-line.
# What a manager must extract: a version, not the URL or line around it.
VERSION = re.compile(r"v?[0-9][0-9A-Za-z.+-]*")
ANNOTATION = re.compile(r"^[ \t]*# renovate: datasource=\S+ depName=\S+.*$", re.MULTILINE)
# Files that hold annotations without being pinned by them: this test's own
# fixtures, and the task library cache downloaded from another repository.
NOT_PINS = ("scripts/renovate-annotations.test.sh",)
NOT_PIN_DIRS = (".task/",)
# The task library: what every Taskfile pins with `?ref=`, and the preset in
# that repository that reads the pins.
TASKLIB_DEP = "oleg-tkachuk/taskfiles"
TASKLIB_PRESET = f"github>{TASKLIB_DEP}//renovate/tasklib"
TASKLIB_ANNOTATION = re.compile(rf"depName={re.escape(TASKLIB_DEP)}(\s|$)")
TASKLIB_PIN = re.compile(rf"github\.com/{re.escape(TASKLIB_DEP)}\.git//[^?\s'\"]*\?ref=")


def js_regex(pattern):
    """A renovate.json regular expression in Python's syntax."""
    return re.compile(re.sub(r"\(\?<(?=[A-Za-z_])", "(?P<", pattern))


def managers(config):
    """The custom managers as (file patterns, match patterns) pairs."""
    out = []
    for m in config.get("customManagers", []):
        files = [js_regex(p[1:-1]) for p in m.get("managerFilePatterns", []) if len(p) > 1 and p[0] == p[-1] == "/"]
        out.append((files, [js_regex(s) for s in m.get("matchStrings", [])]))
    return out


def extracted(path, text, mgrs):
    """Each annotation in text, by line, with the version a manager covering
    path extracts for it; None where none does."""
    out = []
    for found in ANNOTATION.finditer(text):
        start = found.start() + len(found.group(0)) - len(found.group(0).lstrip())
        value = None
        for files, patterns in mgrs:
            if not any(f.search(path) for f in files):
                continue
            for p in patterns:
                m = p.match(text, start)
                if m and m.groupdict().get("currentValue"):
                    value = m.group("currentValue")
        line = text.count("\n", 0, found.start()) + 1
        out.append((f"{path}:{line}: {found.group(0).strip()}", value))
    return out


config = json.load(open(RENOVATE_CONFIG))
mgrs = managers(config)
failed = False

if TASKLIB_PRESET not in config.get("extends", []):
    print(f"FAIL {RENOVATE_CONFIG} does not extend {TASKLIB_PRESET}; nothing reads the task library's pins", file=sys.stderr)
    failed = True

# The forms the repository pins in, so a manager that stops reading one fails
# here rather than silently in Renovate.
cases = [
    (".github/workflows/ci.yaml", '  # renovate: datasource=github-releases depName=go-task/task\n  TASK_VERSION: "3.54.0"\n', "3.54.0"),
    (".github/workflows/ci.yaml", "  # renovate: datasource=github-releases depName=helm/helm\n  HELM_VERSION: v4.3.0\n", "v4.3.0"),
    (".github/actions/setup/action.yaml", '    # renovate: datasource=pypi depName=zizmor\n    ZIZMOR_VERSION: "1.30.1"\n', "1.30.1"),
    ("scripts/example.test.sh", "# renovate: datasource=pypi depName=packaging\nreadonly PACKAGING_VERSION=26.3\n", "26.3"),
    ("frontend/deploy/Dockerfile", "# renovate: datasource=npm depName=pnpm\nARG PNPM_VERSION=12.16.1\n", "12.16.1"),
    ("docs/notes.md", "# renovate: datasource=pypi depName=packaging\nPACKAGING_VERSION=26.3\n", None),
    ("scripts/example.test.sh", "# renovate: datasource=pypi depName=packaging\n# the pin\nreadonly PACKAGING_VERSION=26.3\n", None),
]
for path, text, want in cases:
    [(where, got)] = extracted(path, text, mgrs)
    if got != want:
        print(f"FAIL {where}: extracted {got!r}, want {want!r}", file=sys.stderr)
        failed = True

tracked = subprocess.run(["git", "ls-files", "-z"], capture_output=True, check=True).stdout.decode().split("\0")
count = 0
tasklib_pins = 0
for path in tracked:
    if not path or path in NOT_PINS or path.startswith(NOT_PIN_DIRS):
        continue
    try:
        with open(path, encoding="utf-8") as f:
            text = f.read()
    except (UnicodeDecodeError, IsADirectoryError, FileNotFoundError):
        continue
    tasklib_pins += len(TASKLIB_PIN.findall(text))
    for where, value in extracted(path, text, mgrs):
        count += 1
        if TASKLIB_ANNOTATION.search(where):
            print(f"FAIL {where}: the task library's pin takes no annotation; {TASKLIB_PRESET} already reads it", file=sys.stderr)
            failed = True
            continue
        if value is None or not VERSION.fullmatch(value):
            print(f"FAIL no custom manager in {RENOVATE_CONFIG} reads a version for {where} (got {value!r})", file=sys.stderr)
            failed = True

if count == 0:
    print("FAIL found no annotation at all; the search is broken", file=sys.stderr)
    failed = True
if tasklib_pins == 0:
    print("FAIL found no task library pin at all; the search is broken", file=sys.stderr)
    failed = True
if failed:
    sys.exit(1)
print(f"ok   renovate reads all {count} annotations, and the preset the {tasklib_pins} task library pins")
PY
