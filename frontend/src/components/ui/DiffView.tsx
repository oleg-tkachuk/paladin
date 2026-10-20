"use client";

import { Fragment, useMemo, useState } from "react";
import { CheckCircleIcon } from "@heroicons/react/24/outline";

import { Switch } from "@/components/ui/switch";
import { usePreference } from "@/hooks/usePreference";
import {
  diffHunks,
  diffLines,
  diffStats,
  fullHunk,
  splitRows,
  type DiffHunk,
  type DiffLine,
} from "@/lib/diff";
import { readDiffPrefs, type DiffPrefs } from "@/lib/preferences/diffPrefs";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

// What marks a line besides its colour, so a reader who cannot tell the two
// tints apart, or prints the page, still sees which side a line is on. The
// minus is the typographic one, the width of the plus.
const MARK: Record<DiffLine["kind"], string> = {
  add: "+",
  remove: "−",
  context: " ",
};
// What a screen reader hears in place of the mark.
const SPOKEN: Record<DiffLine["kind"], string> = {
  add: "Added",
  remove: "Removed",
  context: "Unchanged",
};
// The tints are the theme's success and destructive tokens, faint enough that
// the text on them keeps its contrast in both themes.
const ROW_TINT: Record<DiffLine["kind"], string> = {
  add: "bg-success/10",
  remove: "bg-destructive/10",
  context: "",
};
const MARK_TINT: Record<DiffLine["kind"], string> = {
  add: "text-success",
  remove: "text-destructive",
  context: "",
};

const NUMBER_CELL =
  "w-10 select-none px-2 text-right align-top tabular-nums text-muted-foreground";
const MARK_CELL = "w-4 select-none align-top";
const TEXT_CELL = "whitespace-pre-wrap break-all pr-2 align-top";

/** The unified diff's header for a hunk: where it sits in each text. */
function hunkHeader(h: DiffHunk): string {
  return `@@ −${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@`;
}

/**
 * Two versions of a text, line by line: what was removed and what was added,
 * with line numbers from each. After Argo CD's diff: compact shows only the
 * changes and two lines either side, and the two texts can sit in one column
 * or side by side; both choices are the user's preference, kept for every
 * diff in the console.
 */
export function DiffView({
  before,
  after,
  beforeLabel = "Before",
  afterLabel = "After",
  className,
}: {
  before: string;
  after: string;
  /** What the old text is: "Saved", "Before". */
  beforeLabel?: string;
  /** What the new text is: "Draft", "After". */
  afterLabel?: string;
  className?: string;
}) {
  const [stored, , update] = usePreference(PREFERENCE_KEYS.diff, readDiffPrefs);
  // The choice shows at once, and stays for this view even when the settings
  // cannot be written.
  const [chosen, setChosen] = useState<DiffPrefs | null>(null);
  const prefs = chosen ?? stored;
  const choose = (change: Partial<DiffPrefs>) => {
    setChosen({ ...prefs, ...change });
    void update((raw) => ({ ...readDiffPrefs(raw), ...change })).catch(
      () => undefined,
    );
  };

  const lines = useMemo(() => diffLines(before, after), [before, after]);
  const { added, removed } = diffStats(lines);
  const hunks = useMemo(
    () => (prefs.compact ? diffHunks(lines) : [fullHunk(lines)]),
    [lines, prefs.compact],
  );

  if (added === 0 && removed === 0) {
    return (
      <p
        className={cn(
          "flex items-center gap-2 rounded-md border border-border p-3 text-sm text-muted-foreground",
          className,
        )}
      >
        <CheckCircleIcon className="size-4 shrink-0" />
        No differences between {beforeLabel.toLowerCase()} and{" "}
        {afterLabel.toLowerCase()}.
      </p>
    );
  }

  return (
    <div
      className={cn(
        "min-w-0 overflow-hidden rounded-md border border-border",
        className,
      )}
    >
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-border bg-muted/40 px-3 py-2">
        <p className={cn(T.hint, "flex flex-wrap items-center gap-x-3")}>
          <span>
            <span className="font-mono text-destructive" aria-hidden>
              {MARK.remove}
            </span>{" "}
            {beforeLabel}
          </span>
          <span>
            <span className="font-mono text-success" aria-hidden>
              {MARK.add}
            </span>{" "}
            {afterLabel}
          </span>
          <span className="tabular-nums">
            {added} added, {removed} removed
          </span>
        </p>
        <div className="flex items-center gap-4">
          <label className={cn(T.hint, "flex items-center gap-2")}>
            <Switch
              size="sm"
              checked={prefs.compact}
              onCheckedChange={(compact) => choose({ compact })}
              aria-label="Compact"
            />
            Compact
          </label>
          <label className={cn(T.hint, "flex items-center gap-2")}>
            <Switch
              size="sm"
              checked={prefs.split}
              onCheckedChange={(split) => choose({ split })}
              aria-label="Side by side"
            />
            Side by side
          </label>
        </div>
      </div>
      <div className="max-h-96 overflow-auto">
        <table
          className="w-full table-fixed border-collapse font-mono text-xs leading-relaxed"
          aria-label={`Changes from ${beforeLabel.toLowerCase()} to ${afterLabel.toLowerCase()}`}
        >
          {prefs.split ? <SplitColumns /> : <UnifiedColumns />}
          <tbody>
            {hunks.map((h, i) => (
              <Fragment key={`${h.oldStart}:${h.newStart}:${i}`}>
                {prefs.compact ? (
                  <tr className="bg-muted/50 text-muted-foreground">
                    <td colSpan={prefs.split ? 6 : 4} className="px-2 py-0.5">
                      {hunkHeader(h)}
                    </td>
                  </tr>
                ) : null}
                {prefs.split ? (
                  <SplitLines lines={h.lines} />
                ) : (
                  h.lines.map((line, j) => (
                    <UnifiedLine key={`${j}:${line.kind}`} line={line} />
                  ))
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function UnifiedColumns() {
  return (
    <colgroup>
      <col className="w-10" />
      <col className="w-10" />
      <col className="w-4" />
      <col />
    </colgroup>
  );
}

function SplitColumns() {
  return (
    <colgroup>
      <col className="w-10" />
      <col className="w-4" />
      <col />
      <col className="w-10" />
      <col className="w-4" />
      <col />
    </colgroup>
  );
}

/** A line's mark, seen as +/− and heard as what happened to it. */
function Mark({
  kind,
  className,
}: {
  kind: DiffLine["kind"];
  className?: string;
}) {
  return (
    <td className={cn(MARK_CELL, MARK_TINT[kind], className)}>
      <span aria-hidden>{MARK[kind]}</span>
      {kind === "context" ? null : (
        <span className="sr-only">{SPOKEN[kind]}</span>
      )}
    </td>
  );
}

function UnifiedLine({ line }: { line: DiffLine }) {
  return (
    <tr className={ROW_TINT[line.kind]} data-diff={line.kind}>
      <td className={NUMBER_CELL}>{line.oldNumber ?? ""}</td>
      <td className={NUMBER_CELL}>{line.newNumber ?? ""}</td>
      <Mark kind={line.kind} />
      <td className={TEXT_CELL}>{line.text}</td>
    </tr>
  );
}

/** One side of a split row; an empty side is shaded as having no line. */
function Side({
  line,
  number,
}: {
  line: DiffLine | null;
  number: number | null;
}) {
  if (!line) {
    return <td colSpan={3} className="bg-muted/40" />;
  }
  return (
    <>
      <td className={cn(NUMBER_CELL, ROW_TINT[line.kind])}>{number ?? ""}</td>
      <Mark kind={line.kind} className={ROW_TINT[line.kind]} />
      <td className={cn(TEXT_CELL, ROW_TINT[line.kind])}>{line.text}</td>
    </>
  );
}

function SplitLines({ lines }: { lines: DiffLine[] }) {
  return splitRows(lines).map((row, i) => (
    <tr key={i} data-diff={row.left?.kind === "context" ? "context" : "change"}>
      <Side line={row.left} number={row.left?.oldNumber ?? null} />
      <Side line={row.right} number={row.right?.newNumber ?? null} />
    </tr>
  ));
}
