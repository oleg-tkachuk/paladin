#!/usr/bin/env python3
"""Mechanical mutation testing over a Go package: how much of it do the tests
actually hold?

WHAT IT FOUND, so the next person knows what it is for. Run over five packages
it produced: the JWT verifier — the thing that decides whether every request to
every plane is authenticated — with no test file at all; `allowsMissing`, the
allow-list for UNAUTHENTICATED procedures, likewise, where inverting one `&&`
makes every procedure callable without a credential; and the request-validation
interceptor, where inverting one check lets every malformed message through.
Each was a green suite over a guard nothing exercised.

Python rather than shell, in a repository that is otherwise shell. The logic is
line-level text substitution with restore-on-failure, and doing that in bash is
how you get bitten by a `sed` delimiter colliding with the `||` you are trying
to insert — which happened, and quietly reported two mutations as surviving
when they had never been applied. Standard library only; no install step.

USAGE

    scripts/mutate.py ./internal/auth 'internal/auth/*.go' 25
    scripts/mutate.py ./internal/capability/... 'internal/capability/postgres/*.go' 15 \
        --test-cmd 'go test -tags=integration -count=1 -run Capability ./tests/integration/components/'

THE DENOMINATOR IS THE ARGUMENT THAT MATTERS. By default this runs the
package's own tests, and for anything DB-backed that is the wrong measure:
internal/capability/postgres showed 8 survivors, and the one checked by hand is
caught by tests/integration/components. Reporting those as gaps sends someone to write
tests for behaviour that is already held. Pass --test-cmd to measure a package
against the suite that actually covers it.

READING THE OUTPUT

- caught     — the tests failed. The behaviour is held.
- survived   — the tests passed with the code changed. Look at it.
- nocompile  — the mutation does not build. NOT counted either way: it says
               nothing about the tests, and crediting it inflates the score.

A survivor is a question, not a defect. Some are held by a suite this run did
not execute; some are branches whose behaviour genuinely does not matter. The
useful ones are the guards whose whole purpose is to refuse something.
"""

import argparse
import random
import re
import shlex
import subprocess
import sys
from pathlib import Path

# Behaviour-changing and usually compilable: flip a comparison, invert a
# boundary, invert a boolean literal. Deliberately not a full operator matrix —
# these are the ones that map onto "the guard was wrong".
RULES = [
    ("== -> !=", r"(?<![=!<>])== ", "!= "),
    ("!= -> ==", r"!= ", "== "),
    (">= -> >", r">= ", "> "),
    ("<= -> <", r"<= ", "< "),
    ("> -> >=", r"(?<![-=>])> ", ">= "),
    ("&& -> ||", r" && ", " || "),
    ("|| -> &&", r" \|\| ", " && "),
    ("true -> false", r"\btrue\b", "false"),
    ("false -> true", r"\bfalse\b", "true"),
]


def repo_root() -> Path:
    out = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                         capture_output=True, text=True, check=True)
    return Path(out.stdout.strip()) / "backend"


def dirty(root: Path, files) -> list:
    """Files with uncommitted changes.

    Paths are relative to `root`, which is also the cwd git runs in — the first
    version made them relative to the repository root while running from
    backend/, so git looked for backend/backend/... and found nothing dirty
    ever. And an EMPTY list is returned early rather than passed to git:
    `git status --porcelain --` with no paths lists the whole repository, so
    the check fired on unrelated files and never on the ones it guards.
    """
    if not files:
        return []
    rel = [str(f.relative_to(root)) for f in files]
    out = subprocess.run(["git", "status", "--porcelain", "--"] + rel,
                         cwd=root, capture_output=True, text=True)
    return [l[3:] for l in out.stdout.splitlines() if l.strip()]


def run_tests(cmd: str, root: Path, timeout: int) -> str:
    """caught | survived | nocompile.

    `go test` exits 1 for a failing test AND for a package that does not build,
    so the two are separated by looking at the output. Counting a build failure
    as caught would fill the score with mutations the suite never saw.
    """
    try:
        r = subprocess.run(shlex.split(cmd), cwd=root,
                           capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        # A hang is a behaviour change the suite noticed, loudly.
        return "caught"
    out = r.stdout + r.stderr
    if "build failed" in out or "[setup failed]" in out or "typecheck" in out:
        return "nocompile"
    return "survived" if r.returncode == 0 else "caught"


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("package", help="Go package path, e.g. ./internal/auth")
    ap.add_argument("glob", help="source glob relative to backend/, e.g. 'internal/auth/*.go'")
    ap.add_argument("budget", type=int, help="how many mutations to try")
    ap.add_argument("--test-cmd", default=None,
                    help="test command; defaults to `go test -count=1 <package>`. "
                         "Set it for DB-backed packages whose coverage lives elsewhere.")
    ap.add_argument("--timeout", type=int, default=600, help="per-run seconds")
    ap.add_argument("--seed", type=int, default=7, help="site-shuffle seed")
    args = ap.parse_args()

    root = repo_root()
    cmd = args.test_cmd or f"go test -count=1 {args.package}"

    files = sorted(p for p in root.glob(args.glob)
                   if p.suffix == ".go" and not p.name.endswith("_test.go"))
    if not files:
        print(f"!! glob {args.glob!r} matched no source file — nothing was measured",
              file=sys.stderr)
        return 2

    # A run that mutates uncommitted source cannot restore it: the "original"
    # it saves is already your work in progress, and an interrupted run leaves
    # you unable to tell what it changed from what you did.
    if d := dirty(root, files):
        print(f"!! uncommitted changes in the target files: {d}\n"
              f"   commit or stash them — this rewrites these files in place "
              f"and restores from memory, not from git", file=sys.stderr)
        return 2

    sites = []
    for f in files:
        for i, line in enumerate(f.read_text().split("\n")):
            s = line.strip()
            if not s or s.startswith("//") or s.startswith("*"):
                continue
            for name, pat, rep in RULES:
                if re.search(pat, line):
                    sites.append((f, i, name, pat, rep))

    # Zero sites is not a 100% score and not a 0% one — it is a measurement
    # that measured nothing, and printing a percentage for it is how a harness
    # lies. internal/capability produced exactly this: its code lives in a
    # subpackage the glob did not reach.
    if not sites:
        print(f"!! glob {args.glob!r} matched no mutable line — nothing was measured",
              file=sys.stderr)
        return 2

    random.seed(args.seed)
    random.shuffle(sites)
    sites = sites[:args.budget]

    caught = survived = skipped = 0
    survivors = []
    for n, (f, i, name, pat, rep) in enumerate(sites, 1):
        orig = f.read_text()
        lines = orig.split("\n")
        lines[i] = re.sub(pat, rep, lines[i], count=1)
        f.write_text("\n".join(lines))
        try:
            verdict = run_tests(cmd, root, args.timeout)
        finally:
            f.write_text(orig)
        if verdict == "survived":
            survived += 1
            survivors.append((f.relative_to(root), i + 1, name, lines[i].strip()[:100]))
        elif verdict == "caught":
            caught += 1
        else:
            skipped += 1
        print(f"  [{n}/{len(sites)}] {f.name}:{i + 1} {name} -> {verdict}", flush=True)

    # The safety property, asserted rather than assumed: this rewrites source in
    # place, and a run that leaves a mutation behind poisons everything after it.
    if d := dirty(root, files):
        print(f"\n!! MUTATED SOURCE LEFT BEHIND in {d} — restore it with "
              f"`git checkout -- <file>` before doing anything else", file=sys.stderr)
        return 3

    total = caught + survived

    # A run that scored nothing is not a 0% run — it is a run that measured
    # nothing, and printing a percentage for it is the same failure this tool
    # exists to find. It happens when the test command itself cannot start:
    # --test-cmd is split with shlex and executed WITHOUT a shell, so a `&&`
    # or a pipe in it becomes an argument to go test, every mutation "fails to
    # build", and the report reads 0% as though the tests held nothing.
    # Chain commands with `sh -c '...'` instead.
    if total == 0:
        print(f"\n!! nothing was scored: all {skipped} mutations failed to build. "
              f"Usually the test command cannot run at all rather than the "
              f"mutations being invalid — check it in isolation first:\n     {cmd}",
              file=sys.stderr)
        return 4

    # Mutations that do not compile are honest (a swap can be ill-typed), but a
    # majority of them means the denominator is suspect and the percentage is
    # computed over whatever survived the wreckage.
    if skipped > total:
        print(f"\n!! {skipped} of {skipped + total} mutations failed to build — "
              f"more than were scored. Treat the percentage below as unreliable "
              f"until that is explained.", file=sys.stderr)

    pct = 100 * caught / total
    print(f"\n=== {args.package}: caught {caught}, survived {survived}, "
          f"did not compile {skipped} (not counted) — {pct:.0f}%")
    for path, line, rule, text in survivors:
        print(f"  SURVIVED  {path}:{line}  [{rule}]  {text}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
