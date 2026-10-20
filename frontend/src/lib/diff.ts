// A line diff of two texts, for showing what an edit changes: a saved policy
// against its draft, one snapshot of a record against the next. Pure, so the
// viewer only lays out what this computes.
//
// The algorithm is Myers' O(ND) shortest edit script (Myers 1986, "An O(ND)
// Difference Algorithm and Its Variations"). It finds the fewest lines added
// and removed, which is what a reader expects of a diff, and it is quick when
// the two texts are mostly alike, which is the usual case here: an edit to a
// policy, a field or two changed on a record.

/** What a line of a diff is: in both texts, only in the new, only in the old. */
export type DiffLineKind = "context" | "add" | "remove";

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
  /** The line's number in the old text; null for an added line. */
  oldNumber: number | null;
  /** The line's number in the new text; null for a removed line. */
  newNumber: number | null;
}

/** A run of changes with the unchanged lines around them. */
export interface DiffHunk {
  /** The first old line the hunk shows, counted from 1; 0 when it shows none. */
  oldStart: number;
  oldLines: number;
  /** The first new line the hunk shows, counted from 1; 0 when it shows none. */
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}

/** A row of a side-by-side diff: the old line on the left, the new on the right. */
export interface SplitRow {
  left: DiffLine | null;
  right: DiffLine | null;
}

/** How many unchanged lines a compact diff keeps either side of a change. */
export const DIFF_CONTEXT_LINES = 2;

/**
 * The text's lines. Windows and old Mac line endings read as one, so a text
 * saved on another system does not differ on every line; a final line ending
 * closes the last line rather than opening an empty one, so an editor that
 * adds one on save does not leave a difference nobody can see.
 */
function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.split(/\r\n|\r|\n/);
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

/** The edits that turn a into b, as Myers finds them, from first to last. */
type Edit = "equal" | "insert" | "delete";

/**
 * The shortest edit script from a to b, the lines compared by identity. Each
 * step of the search keeps only the diagonals it could have reached, so the
 * trace grows with the square of the number of edits, not of the lengths.
 */
function editScript(a: readonly number[], b: readonly number[]): Edit[] {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  const offset = max + 1;
  // v[k + offset] is the furthest x reached on diagonal k = x - y.
  const v = new Int32Array(2 * max + 3);
  const trace: Int32Array[] = [];
  let found = false;
  for (let d = 0; d <= max && !found; d++) {
    trace.push(v.slice(offset - d - 1, offset + d + 2));
    for (let k = -d; k <= d; k += 2) {
      const down =
        k === -d || (k !== d && v[offset + k - 1] < v[offset + k + 1]);
      let x = down ? v[offset + k + 1] : v[offset + k - 1] + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[offset + k] = x;
      if (x >= n && y >= m) {
        found = true;
        break;
      }
    }
  }

  // Walked back from the end: each step of the trace undoes one edit and the
  // run of equal lines after it.
  const edits: Edit[] = [];
  let x = n;
  let y = m;
  for (let d = trace.length - 1; d >= 0; d--) {
    const saved = trace[d];
    // saved holds diagonals -d - 1 to d + 1.
    const at = (k: number) => saved[k + d + 1];
    const k = x - y;
    const down = k === -d || (k !== d && at(k - 1) < at(k + 1));
    const prevK = down ? k + 1 : k - 1;
    const prevX = at(prevK);
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      edits.push("equal");
      x--;
      y--;
    }
    if (d > 0) edits.push(down ? "insert" : "delete");
    x = prevX;
    y = prevY;
  }
  return edits.reverse();
}

/**
 * Every line of both texts, in order, each marked as unchanged, added or
 * removed. Where a block of lines was replaced, the removed lines come before
 * the added ones, as a reader compares them.
 */
export function diffLines(before: string, after: string): DiffLine[] {
  const oldLines = splitLines(before);
  const newLines = splitLines(after);

  // The common head and tail are not searched: an edit usually touches a few
  // lines in the middle of a long text.
  let head = 0;
  while (
    head < oldLines.length &&
    head < newLines.length &&
    oldLines[head] === newLines[head]
  ) {
    head++;
  }
  let tail = 0;
  while (
    tail < oldLines.length - head &&
    tail < newLines.length - head &&
    oldLines[oldLines.length - 1 - tail] ===
      newLines[newLines.length - 1 - tail]
  ) {
    tail++;
  }

  // Lines are compared as numbers, one per distinct text.
  const ids = new Map<string, number>();
  const intern = (line: string) => {
    let id = ids.get(line);
    if (id === undefined) {
      id = ids.size;
      ids.set(line, id);
    }
    return id;
  };
  const a = oldLines.slice(head, oldLines.length - tail).map(intern);
  const b = newLines.slice(head, newLines.length - tail).map(intern);

  const out: DiffLine[] = [];
  let o = 0;
  let n = 0;
  const context = () => {
    out.push({
      kind: "context",
      text: oldLines[o],
      oldNumber: o + 1,
      newNumber: n + 1,
    });
    o++;
    n++;
  };
  for (let i = 0; i < head; i++) context();

  // Removed and added lines are held until the run of changes ends, so the
  // run reads removals first whatever order the search found them in.
  let removed: DiffLine[] = [];
  let added: DiffLine[] = [];
  const flush = () => {
    out.push(...removed, ...added);
    removed = [];
    added = [];
  };
  for (const edit of editScript(a, b)) {
    if (edit === "equal") {
      flush();
      context();
    } else if (edit === "delete") {
      removed.push({
        kind: "remove",
        text: oldLines[o],
        oldNumber: o + 1,
        newNumber: null,
      });
      o++;
    } else {
      added.push({
        kind: "add",
        text: newLines[n],
        oldNumber: null,
        newNumber: n + 1,
      });
      n++;
    }
  }
  flush();
  for (let i = 0; i < tail; i++) context();
  return out;
}

/** How many lines the diff adds and removes. */
export function diffStats(lines: readonly DiffLine[]): {
  added: number;
  removed: number;
} {
  let added = 0;
  let removed = 0;
  for (const l of lines) {
    if (l.kind === "add") added++;
    else if (l.kind === "remove") removed++;
  }
  return { added, removed };
}

/** The hunk that shows lines[from, to). */
function hunkOf(
  lines: readonly DiffLine[],
  from: number,
  to: number,
): DiffHunk {
  const shown = lines.slice(from, to);
  const oldShown = shown.filter((l) => l.oldNumber !== null);
  const newShown = shown.filter((l) => l.newNumber !== null);
  // A hunk with no lines on one side starts, as in a unified diff, at the
  // line before the gap: 0 when the gap is at the top.
  const before = lines.slice(0, from);
  const lastOld = before.findLast((l) => l.oldNumber !== null)?.oldNumber ?? 0;
  const lastNew = before.findLast((l) => l.newNumber !== null)?.newNumber ?? 0;
  return {
    oldStart: oldShown[0]?.oldNumber ?? lastOld,
    oldLines: oldShown.length,
    newStart: newShown[0]?.newNumber ?? lastNew,
    newLines: newShown.length,
    lines: shown,
  };
}

/**
 * The changes with `context` unchanged lines either side of each, as hunks.
 * Changes whose context would meet or overlap share a hunk, so no unchanged
 * line is shown twice and none is hidden between two that are shown. A diff
 * that changes nothing has no hunks.
 */
export function diffHunks(
  lines: readonly DiffLine[],
  context: number = DIFF_CONTEXT_LINES,
): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  let start = -1;
  let end = -1;
  lines.forEach((line, i) => {
    if (line.kind === "context") return;
    const from = Math.max(0, i - context);
    const to = Math.min(lines.length, i + context + 1);
    if (start >= 0 && from <= end) {
      end = Math.max(end, to);
      return;
    }
    if (start >= 0) hunks.push(hunkOf(lines, start, end));
    start = from;
    end = to;
  });
  if (start >= 0) hunks.push(hunkOf(lines, start, end));
  return hunks;
}

/** The whole diff as one hunk, for a view that hides nothing. */
export function fullHunk(lines: readonly DiffLine[]): DiffHunk {
  return hunkOf(lines, 0, lines.length);
}

/**
 * The lines in two columns. An unchanged line sits on both sides; a run of
 * removed lines is paired, row by row, with the run of added lines that
 * replaced it, and the longer run's extra lines face an empty cell.
 */
export function splitRows(lines: readonly DiffLine[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.kind === "context") {
      rows.push({ left: line, right: line });
      i++;
      continue;
    }
    const removed: DiffLine[] = [];
    const added: DiffLine[] = [];
    while (i < lines.length && lines[i].kind === "remove")
      removed.push(lines[i++]);
    while (i < lines.length && lines[i].kind === "add") added.push(lines[i++]);
    for (let r = 0; r < Math.max(removed.length, added.length); r++) {
      rows.push({ left: removed[r] ?? null, right: added[r] ?? null });
    }
  }
  return rows;
}
