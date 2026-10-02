"""The version a build without git reads: .git_archival.txt, which git archive
fills in, must describe the same tags the build's own git_describe matches."""

from __future__ import annotations

import re
from pathlib import Path

SDK = Path(__file__).resolve().parents[1]
ROOT = SDK.parents[1]
ARCHIVAL = ROOT / ".git_archival.txt"
ATTRIBUTES = ROOT / ".gitattributes"
# The local label marks a build that could not learn its version.
UNKNOWN_LOCAL_LABEL = "+unknown"


def _setting(name: str) -> str:
    # tomllib arrives in Python 3.11; the SDK supports 3.10.
    found = re.search(rf"^{name} = (.+)$", (SDK / "pyproject.toml").read_text(), re.MULTILINE)
    assert found, f"pyproject.toml sets no {name}"
    return found.group(1)


def _match_pattern() -> str:
    found = re.search(r'"--match", "([^"]+)"', _setting("git_describe_command"))
    assert found, "git_describe_command has no --match"
    return found.group(1)


def test_the_archive_describes_the_tags_the_build_matches() -> None:
    pattern = _match_pattern()
    describe = re.search(r"describe-name: \$Format:%\(describe:(.*)\)\$", ARCHIVAL.read_text())
    assert describe, ".git_archival.txt has no describe-name placeholder"
    assert f"match={pattern}" in describe.group(1).split(",")


def test_git_archive_fills_the_file_in() -> None:
    rules = [line.split() for line in ATTRIBUTES.read_text().splitlines()]
    assert [ARCHIVAL.name, "export-subst"] in rules


def test_a_build_without_a_version_says_so() -> None:
    assert _setting("fallback_version").strip('"').endswith(UNKNOWN_LOCAL_LABEL)
