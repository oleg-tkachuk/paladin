#!/usr/bin/env python3
"""Check that each chart's values.schema.json admits everything its templates read.

The schemas were generated from the values files we ship, so they encode what we
set rather than what the chart supports. That is narrower, and it fails in the
direction nobody sees until deploy day: an operator sets a key the templates
read and gets `additional properties … not allowed` from Helm. It has already
happened three times — `deployments.<role>` carried five different shapes for
one thing the templates read uniformly, `targetMemoryUtilizationPercentage` was
read and refused, and six objects shipped as `{}` were closed to no keys at all.

Each of those was found by hand. This does it mechanically, and refuses to
guess: a variable binding whose shape it cannot classify is an error, not a
silent skip, so the checker cannot quietly stop covering the templates it grew
up with.

    python3 scripts/chart-reads.py                # check every chart
    python3 scripts/chart-reads.py --report       # also print what resolved
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

# ── schema ──────────────────────────────────────────────────────────────────


class Node:
    """A schema node, plus how to walk it the way a template walks values."""

    def __init__(self, raw: dict | None, path: str):
        self.raw = raw if isinstance(raw, dict) else None
        self.path = path

    @property
    def open(self) -> bool:
        """True when the node constrains nothing — checks below it are vacuous."""
        return self.raw is None or self.raw.get("additionalProperties") is not False

    def child(self, key: str) -> "Node":
        props = (self.raw or {}).get("properties") or {}
        return Node(props.get(key), f"{self.path}.{key}")

    def has(self, key: str) -> bool:
        return key in ((self.raw or {}).get("properties") or {})

    def element(self) -> "Node":
        """The schema of one member: array items, or the union of a map's values."""
        if self.raw is None:
            return Node(None, f"{self.path}[]")
        if self.raw.get("type") == "array":
            return Node(self.raw.get("items"), f"{self.path}[]")
        merged: dict = {}
        for spec in ((self.raw.get("properties") or {}).values()):
            merged = union(merged, spec)
        extra = self.raw.get("additionalProperties")
        if isinstance(extra, dict):
            merged = union(merged, extra)
        elif extra is not False and not merged:
            return Node(None, f"{self.path}[*]")
        return Node(merged or None, f"{self.path}[*]")


def union(a: dict, b: dict) -> dict:
    """Permissive merge — a key legal in either member is legal in the element."""
    if not a:
        return dict(b)
    if not b:
        return dict(a)
    out = {"type": a.get("type") or b.get("type")}
    props = dict(a.get("properties") or {})
    for k, v in (b.get("properties") or {}).items():
        props[k] = union(props.get(k) or {}, v)
    if props:
        out["properties"] = props
    out["additionalProperties"] = (
        a.get("additionalProperties") is not False and b.get("additionalProperties") is not False
    ) or (a.get("additionalProperties") is False) != (b.get("additionalProperties") is False)
    return out


def resolve(root: Node, dotted: str) -> Node | None:
    node = root
    for seg in dotted.split("."):
        if not node.has(seg):
            return None if not node.open else Node(None, f"{node.path}.{seg}")
        node = node.child(seg)
    return node


# ── templates ───────────────────────────────────────────────────────────────

VALUES = r"\$?\.Values\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)"

# Bindings whose right-hand side is not a values subtree at all. Listed by the
# function or accessor that produces them, so a new one is an error rather than
# an assumption: `include` and `lookup` return rendered text and cluster state,
# `dict`/`list`/`printf`/`ternary`/`concat` construct, and a bare `.field` is a
# key of the dict a helper was called with — none of which the schema describes.
NOT_VALUES = re.compile(
    r"^\(?\s*(?:"
    r"dict\b|list\b|include\b|lookup\b|fromJson\b|printf\b|ternary\b|concat\b|"
    r"int\b|or\b|and\b|not\b|empty\b|"
    r"true\b|false\b|\"|'|`|-?\d|"
    r"\.[A-Za-z_]|"          # .ctx / .role / .spec — a helper's argument
    r"\$[A-Za-z_][A-Za-z0-9_]*\s*$|"   # alias of another local
    r"\.Release\b|\.Chart\b|\.Capabilities\b|\.Template\b|\.Files\b"
    r")"
)


def bindings(text: str) -> list[tuple[str, str, bool]]:
    """(name, rhs, is_element) for every `$x := …`, range or not."""
    out: list[tuple[str, str, bool]] = []
    for m in re.finditer(
        r"(range\s+)?\$([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*\$([A-Za-z_][A-Za-z0-9_]*)\s*)?:=\s*([^}]*?)\s*-?\}\}",
        text,
    ):
        is_range = bool(m.group(1))
        first, second, rhs = m.group(2), m.group(3), m.group(4)
        name = second or first
        # `range $k, $v := xs` binds $v to a member; `range $v := xs` binds the
        # member too — the single-variable form is the value, not the index.
        out.append((name, rhs.strip(), is_range))
    return out


def reads(text: str, var: str) -> set[str]:
    return set(re.findall(r"\$" + var + r"\.([A-Za-z_][A-Za-z0-9_]*)", text))


def direct_reads(text: str) -> set[str]:
    return set(re.findall(VALUES, text))


def bind(rhs: str, env: dict[str, Node], root: Node) -> Node | None | str:
    """Resolve a binding's right-hand side.

    Returns a Node when it names a values subtree, None when it demonstrably
    names something else, and a string (the reason) when the shape is one this
    checker does not understand — which is a failure, not a skip.
    """
    rhs = rhs.strip()
    if rhs.startswith("(") and rhs.endswith(")"):
        rhs = rhs[1:-1].strip()

    # `a | default b` / `deepCopy a` — the pipeline's subject is what matters.
    rhs = re.sub(r"^deepCopy\s+", "", rhs)
    head = rhs.split("|")[0].strip()

    # `default A B` and `index A K` reach through to their operands.
    if head.startswith("default "):
        parts = [p for p in re.split(r"\s+", head[len("default "):].strip()) if p]
        nodes = [bind(p, env, root) for p in parts]
        real = [n for n in nodes if isinstance(n, Node)]
        if not real:
            return None
        merged = real[0].raw or {}
        for n in real[1:]:
            merged = union(merged, n.raw or {})
        return Node(merged or None, " | ".join(n.path for n in real))
    if head.startswith("index "):
        subject = re.split(r"\s+", head[len("index "):].strip())[0]
        node = bind(subject, env, root)
        return node.element() if isinstance(node, Node) else node

    m = re.match(r"^" + VALUES + r"$", head)
    if m:
        node = resolve(root, m.group(1))
        return node if node is not None else f"no schema node for .Values.{m.group(1)}"

    m = re.match(r"^\$([A-Za-z_][A-Za-z0-9_]*)((?:\.[A-Za-z_][A-Za-z0-9_]*)*)$", head)
    if m and m.group(1) in env:
        node = env[m.group(1)]
        for seg in [s for s in m.group(2).split(".") if s]:
            node = node.child(seg)
        return node

    if NOT_VALUES.match(head) or head in ("$", ".", ""):
        return None
    return f"unclassified binding: {head}"


# ── the check ───────────────────────────────────────────────────────────────


def check_chart(chart: Path, report: bool) -> list[str]:
    schema = json.loads((chart / "values.schema.json").read_text())
    root = Node(schema, "")
    problems: list[str] = []
    resolved_total = 0
    checked_paths = 0

    for f in sorted(list((chart / "templates").glob("*.yaml")) + list((chart / "templates").glob("*.tpl"))):
        text = f.read_text()
        rel = f"{chart.parent.parent.name}/{f.name}"

        # A variable is worth resolving only if something dereferences it.
        wanted = {name for name, _, _ in bindings(text) if reads(text, name)}
        env: dict[str, Node] = {}
        # Fixpoint: `$auto := default $defaults.autoscaling $spec.autoscaling`
        # cannot resolve before $defaults and $spec do.
        for _ in range(len(wanted) + 1):
            for name, rhs, is_range in bindings(text):
                if name not in wanted or name in env:
                    continue
                got = bind(rhs, env, root)
                if isinstance(got, str):
                    continue
                if got is None:
                    env[name] = Node(None, "(not values)")
                    continue
                env[name] = got.element() if is_range else got

        for name, rhs, _ in bindings(text):
            if name in wanted and name not in env:
                got = bind(rhs, env, root)
                if isinstance(got, str):
                    problems.append(f"{rel}: ${name} — {got}")

        for name, node in env.items():
            if node.path == "(not values)" or node.open:
                continue
            resolved_total += 1
            for field in sorted(reads(text, name)):
                if not node.has(field):
                    problems.append(
                        f"{rel}: ${name}.{field} is read but the schema refuses it "
                        f"(${name} is {node.path})"
                    )
            if report:
                print(f"  {rel}: ${name} -> {node.path}")

        for dotted in sorted(direct_reads(text)):
            node = resolve(root, dotted)
            if node is None:
                problems.append(f"{rel}: .Values.{dotted} is read but the schema refuses it")
            elif node.raw is not None:
                checked_paths += 1

    if report:
        # Both numbers, because either alone can be zero legitimately: the
        # console chart binds no values subtree at all and is covered entirely
        # by direct reads.
        print(f"  ({resolved_total} constrained bindings, {checked_paths} direct paths checked)")

    # Zero on both means the regexes matched nothing — a template syntax this
    # file does not recognise, or a rename — and every check above was a
    # vacuous truth. A checker that has stopped reading the templates must say
    # so, not report a clean chart.
    if resolved_total == 0 and checked_paths == 0:
        problems.append("nothing was checked — the templates parsed to no values reads at all")
    return problems


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--report", action="store_true", help="print what each variable resolved to")
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"],
                               capture_output=True, text=True, check=True).stdout.strip())
    charts = [root / "backend/deploy/chart", root / "frontend/deploy/chart"]

    failed = False
    for chart in charts:
        name = chart.parent.parent.name
        if args.report:
            print(f"{name}:")
        problems = check_chart(chart, args.report)
        if problems:
            failed = True
            print(f"!!! {name}: the values schema does not admit what the templates read", file=sys.stderr)
            for p in problems:
                print(f"      {p}", file=sys.stderr)
        else:
            print(f"{name}: every value the templates read is admitted by the schema")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
