import { describe, expect, it } from "vitest";

import {
  diffHunks,
  diffLines,
  diffStats,
  fullHunk,
  splitRows,
  type DiffLine,
} from "./diff";

/** The diff in a unified diff's notation: " a", "-b", "+c". */
function unified(lines: readonly DiffLine[]): string[] {
  const mark = { context: " ", add: "+", remove: "-" } as const;
  return lines.map((l) => `${mark[l.kind]}${l.text}`);
}

/** True when the diff changes nothing. */
const isUnchanged = (lines: readonly DiffLine[]) =>
  lines.every((l) => l.kind === "context");

/** Applies the diff's new side, as a check that it rebuilds the new text. */
const newSide = (lines: readonly DiffLine[]) =>
  lines.filter((l) => l.kind !== "remove").map((l) => l.text);
const oldSide = (lines: readonly DiffLine[]) =>
  lines.filter((l) => l.kind !== "add").map((l) => l.text);

describe("diffLines", () => {
  it("has no lines for two empty texts", () => {
    const lines = diffLines("", "");
    expect(lines).toEqual([]);
    expect(isUnchanged(lines)).toBe(true);
    expect(diffHunks(lines)).toEqual([]);
  });

  it("marks every line of identical texts unchanged, numbered on both sides", () => {
    const lines = diffLines("a\nb\nc", "a\nb\nc");
    expect(unified(lines)).toEqual([" a", " b", " c"]);
    expect(lines.map((l) => [l.oldNumber, l.newNumber])).toEqual([
      [1, 1],
      [2, 2],
      [3, 3],
    ]);
    expect(isUnchanged(lines)).toBe(true);
    expect(diffHunks(lines)).toEqual([]);
  });

  it("adds every line to an empty text", () => {
    const lines = diffLines("", "a\nb");
    expect(unified(lines)).toEqual(["+a", "+b"]);
    expect(lines.map((l) => l.oldNumber)).toEqual([null, null]);
    expect(lines.map((l) => l.newNumber)).toEqual([1, 2]);
    expect(diffStats(lines)).toEqual({ added: 2, removed: 0 });
    expect(diffHunks(lines)).toEqual([
      { oldStart: 0, oldLines: 0, newStart: 1, newLines: 2, lines },
    ]);
  });

  it("removes every line when the new text is empty", () => {
    const lines = diffLines("a\nb\n", "");
    expect(unified(lines)).toEqual(["-a", "-b"]);
    expect(lines.map((l) => l.newNumber)).toEqual([null, null]);
    expect(diffStats(lines)).toEqual({ added: 0, removed: 2 });
    expect(diffHunks(lines)[0]).toMatchObject({
      oldStart: 1,
      oldLines: 2,
      newStart: 0,
      newLines: 0,
    });
  });

  it("does not count a final line ending as a line or a difference", () => {
    expect(isUnchanged(diffLines("a\nb\n", "a\nb"))).toBe(true);
    expect(diffLines("a\n", "a\n")).toHaveLength(1);
  });

  it("keeps an empty line before the final line ending", () => {
    expect(unified(diffLines("a\n", "a\n\n"))).toEqual([" a", "+"]);
  });

  it("reads CRLF and CR line endings as LF", () => {
    expect(isUnchanged(diffLines("a\r\nb\r\n", "a\nb\n"))).toBe(true);
    expect(isUnchanged(diffLines("a\rb", "a\nb"))).toBe(true);
    expect(unified(diffLines("a\r\nb\r\n", "a\nc\n"))).toEqual([
      " a",
      "-b",
      "+c",
    ]);
  });

  it("puts removed lines before the lines that replaced them", () => {
    const lines = diffLines("a\nb\nc\nd", "a\nx\ny\nd");
    expect(unified(lines)).toEqual([" a", "-b", "-c", "+x", "+y", " d"]);
  });

  it("numbers each side's lines in its own text", () => {
    const lines = diffLines("a\nb\nc", "a\nx\nb\nc");
    expect(unified(lines)).toEqual([" a", "+x", " b", " c"]);
    expect(lines.map((l) => [l.oldNumber, l.newNumber])).toEqual([
      [1, 1],
      [null, 2],
      [2, 3],
      [3, 4],
    ]);
  });

  it("finds the shortest edit, not a rewrite", () => {
    // The textbook pair from Myers' paper: five edits, four lines kept.
    const lines = diffLines("A\nB\nC\nA\nB\nB\nA", "C\nB\nA\nB\nA\nC");
    const { added, removed } = diffStats(lines);
    expect(added + removed).toBe(5);
    expect(lines.filter((l) => l.kind === "context")).toHaveLength(4);
  });

  it("rebuilds both texts from its sides", () => {
    const before = "permit (\n  principal,\n  action,\n  resource\n);\n";
    const after =
      '// tightened\npermit (\n  principal in Group::"ops",\n  action,\n  resource\n)\nwhen { true };\n';
    const lines = diffLines(before, after);
    expect(oldSide(lines)).toEqual(before.trimEnd().split("\n"));
    expect(newSide(lines)).toEqual(after.trimEnd().split("\n"));
  });

  it("handles repeated lines without losing any", () => {
    const before = "x\nx\nx\ny";
    const after = "y\nx\nx";
    const lines = diffLines(before, after);
    expect(oldSide(lines)).toEqual(["x", "x", "x", "y"]);
    expect(newSide(lines)).toEqual(["y", "x", "x"]);
  });

  it("diffs a long text with a change in the middle", () => {
    const before = Array.from({ length: 2000 }, (_, i) => `line ${i}`);
    const after = [...before];
    after[1000] = "changed";
    const lines = diffLines(before.join("\n"), after.join("\n"));
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 });
    expect(lines).toHaveLength(2001);
  });

  it("diffs two unrelated texts as all removed then all added", () => {
    const lines = diffLines("a\nb", "c\nd");
    expect(unified(lines)).toEqual(["-a", "-b", "+c", "+d"]);
  });
});

describe("diffHunks", () => {
  const before = Array.from({ length: 20 }, (_, i) => `l${i + 1}`).join("\n");

  it("keeps two unchanged lines either side of a change", () => {
    const after = before.replace("l10", "ten");
    const hunks = diffHunks(diffLines(before, after));
    expect(hunks).toHaveLength(1);
    expect(unified(hunks[0].lines)).toEqual([
      " l8",
      " l9",
      "-l10",
      "+ten",
      " l11",
      " l12",
    ]);
    expect(hunks[0]).toMatchObject({
      oldStart: 8,
      oldLines: 5,
      newStart: 8,
      newLines: 5,
    });
  });

  it("splits changes far apart into separate hunks", () => {
    const after = before.replace("l3", "three").replace("l17", "seventeen");
    const hunks = diffHunks(diffLines(before, after));
    expect(hunks).toHaveLength(2);
    expect(hunks[0].oldStart).toBe(1);
    expect(hunks[1].oldStart).toBe(15);
  });

  it("joins changes whose context would meet", () => {
    // l5 and l10 changed: l6-l7 and l8-l9 are each change's context, so
    // nothing between them is hidden.
    const after = before.replace("l5", "five").replace("l10", "ten");
    const hunks = diffHunks(diffLines(before, after));
    expect(hunks).toHaveLength(1);
    expect(hunks[0].oldStart).toBe(3);
    expect(hunks[0].oldLines).toBe(10);
  });

  it("keeps changes one hidden line apart separate", () => {
    const after = before.replace("l5", "five").replace("l11", "eleven");
    expect(diffHunks(diffLines(before, after))).toHaveLength(2);
  });

  it("stops the context at the edges of the text", () => {
    const after = before.replace("l1\n", "one\n").replace("l20", "twenty");
    const hunks = diffHunks(diffLines(before, after));
    expect(unified(hunks[0].lines)).toEqual(["-l1", "+one", " l2", " l3"]);
    expect(unified(hunks[1].lines)).toEqual([
      " l18",
      " l19",
      "-l20",
      "+twenty",
    ]);
  });

  it("takes a context width", () => {
    const after = before.replace("l10", "ten");
    const [hunk] = diffHunks(diffLines(before, after), 0);
    expect(unified(hunk.lines)).toEqual(["-l10", "+ten"]);
  });

  it("starts an insertion's empty side at the line before it", () => {
    const [hunk] = diffHunks(
      diffLines("a\nb\nc\nd\ne\nf", "a\nb\nc\nd\nX\ne\nf"),
      0,
    );
    expect(hunk).toMatchObject({
      oldStart: 4,
      oldLines: 0,
      newStart: 5,
      newLines: 1,
    });
  });
});

describe("fullHunk", () => {
  it("shows every line", () => {
    const lines = diffLines("a\nb\nc", "a\nB\nc");
    expect(fullHunk(lines)).toEqual({
      oldStart: 1,
      oldLines: 3,
      newStart: 1,
      newLines: 3,
      lines,
    });
  });
});

describe("splitRows", () => {
  it("puts an unchanged line on both sides", () => {
    const [row] = splitRows(diffLines("a", "a"));
    expect(row.left?.text).toBe("a");
    expect(row.right).toBe(row.left);
  });

  it("pairs removed lines with the lines that replaced them", () => {
    const rows = splitRows(diffLines("a\nb\nc\nz", "a\nx\nz"));
    expect(
      rows.map((r) => [r.left?.text ?? null, r.right?.text ?? null]),
    ).toEqual([
      ["a", "a"],
      ["b", "x"],
      ["c", null],
      ["z", "z"],
    ]);
  });

  it("faces an added line with an empty cell", () => {
    const rows = splitRows(diffLines("a", "a\nb"));
    expect(rows[1]).toEqual({
      left: null,
      right: expect.objectContaining({ kind: "add", text: "b" }),
    });
  });
});

describe("diffLines against a brute-force longest common subsequence", () => {
  // A seeded generator, so a failure names the case it failed on.
  function random(seed: number) {
    let s = seed;
    return () => {
      s = (s * 1103515245 + 12345) % 2147483648;
      return s / 2147483648;
    };
  }
  function lcs(a: string[], b: string[]): number {
    const t = Array.from({ length: a.length + 1 }, () =>
      new Array<number>(b.length + 1).fill(0),
    );
    for (let i = 1; i <= a.length; i++) {
      for (let j = 1; j <= b.length; j++) {
        t[i][j] =
          a[i - 1] === b[j - 1]
            ? t[i - 1][j - 1] + 1
            : Math.max(t[i - 1][j], t[i][j - 1]);
      }
    }
    return t[a.length][b.length];
  }

  it("is minimal and rebuilds both texts for many random pairs", () => {
    const next = random(42);
    const text = () =>
      Array.from({ length: Math.floor(next() * 12) }, () =>
        "abcd".charAt(Math.floor(next() * 4)),
      );
    for (let c = 0; c < 300; c++) {
      const a = text();
      const b = text();
      const lines = diffLines(a.join("\n"), b.join("\n"));
      expect(oldSide(lines), `case ${c}`).toEqual(a);
      expect(newSide(lines), `case ${c}`).toEqual(b);
      expect(
        lines.filter((l) => l.kind === "context"),
        `case ${c}`,
      ).toHaveLength(lcs(a, b));
    }
  });
});
