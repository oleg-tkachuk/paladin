"""The version a build without git reads: .git_archival.txt, which git archive
fills in, must describe the same tags the build's own git_describe matches;
and an install whose build could not learn it still reports the tag it was
installed from."""

from __future__ import annotations

import json
import re
import tomllib
from importlib import metadata
from pathlib import Path
from typing import Any

import pytest

from paladin import client
from paladin.client import RELEASE_TAG, UNKNOWN_VERSION_LABEL, sdk_version

SDK = Path(__file__).resolve().parents[1]
ROOT = SDK.parents[1]
ARCHIVAL = ROOT / ".git_archival.txt"
ATTRIBUTES = ROOT / ".gitattributes"
TAG = "sdk/go/v0.23.0"
RELEASE = "0.23.0"
UNKNOWN = "0.0.0" + UNKNOWN_VERSION_LABEL
COMMIT = "19d4c8f6e586bd5e1a71c5ef471e2db7caa93904"


def _setting(name: str) -> Any:
    raw_options = tomllib.loads((SDK / "pyproject.toml").read_text())["tool"]["hatch"]["version"][
        "raw-options"
    ]
    assert name in raw_options, f"pyproject.toml sets no {name}"
    return raw_options[name]


def _match_pattern() -> str:
    command: list[str] = _setting("git_describe_command")
    assert "--match" in command, "git_describe_command has no --match"
    return command[command.index("--match") + 1]


def test_the_archive_describes_the_tags_the_build_matches() -> None:
    pattern = _match_pattern()
    describe = re.search(r"describe-name: \$Format:%\(describe:(.*)\)\$", ARCHIVAL.read_text())
    assert describe, ".git_archival.txt has no describe-name placeholder"
    assert f"match={pattern}" in describe.group(1).split(",")


def test_git_archive_fills_the_file_in() -> None:
    rules = [line.split() for line in ATTRIBUTES.read_text().splitlines()]
    assert [ARCHIVAL.name, "export-subst"] in rules


def test_a_build_without_a_version_says_so() -> None:
    assert _setting("fallback_version").endswith(UNKNOWN_VERSION_LABEL)


def test_the_code_reads_tags_as_the_build_does() -> None:
    assert RELEASE_TAG.pattern == _setting("tag_regex")


def _direct_url(revision: str | None) -> str:
    """What Poetry records for an install from a git tag."""
    vcs: dict[str, Any] = {"vcs": "git", "commit_id": COMMIT}
    if revision is not None:
        vcs["requested_revision"] = revision
    return json.dumps(
        {
            "url": "https://github.com/oleg-tkachuk/paladin.git",
            "vcs_info": vcs,
            "subdirectory": "sdk/python",
        }
    )


@pytest.mark.parametrize(
    ("text", "want"),
    [
        (_direct_url(TAG), RELEASE),
        (_direct_url("main"), None),
        (_direct_url(COMMIT), None),
        (_direct_url("sdk/go/v0.23"), None),
        (_direct_url("v0.23.0"), None),
        (_direct_url(None), None),
        (json.dumps({"url": "file:///src/sdk/python", "dir_info": {}}), None),
        ("not json", None),
        ("[]", None),
        ("", None),
        (None, None),
    ],
    ids=[
        "a release tag",
        "a branch",
        "a commit",
        "a partial tag",
        "the product's tag",
        "no revision",
        "a local directory",
        "not JSON",
        "not an object",
        "empty",
        "no file",
    ],
)
def test_the_version_from_direct_url(text: str | None, want: str | None) -> None:
    assert client._version_from_direct_url(text) == want


class _Dist:
    def __init__(self, version: str, direct_url: str | None) -> None:
        self.version, self._direct_url = version, direct_url

    def read_text(self, name: str) -> str | None:
        return self._direct_url if name == "direct_url.json" else None


@pytest.mark.parametrize(
    ("built", "direct_url", "want"),
    [
        (UNKNOWN, _direct_url(TAG), RELEASE),
        (UNKNOWN, _direct_url("main"), UNKNOWN),
        ("0.22.0", _direct_url(TAG), "0.22.0"),
    ],
    ids=["unknown, installed from a tag", "unknown, from a branch", "known: kept"],
)
def test_sdk_version(
    monkeypatch: pytest.MonkeyPatch, built: str, direct_url: str, want: str
) -> None:
    monkeypatch.setattr(metadata, "distribution", lambda name: _Dist(built, direct_url))
    sdk_version.cache_clear()
    try:
        assert sdk_version() == want
    finally:
        sdk_version.cache_clear()
