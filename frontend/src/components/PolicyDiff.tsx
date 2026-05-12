"use client";

// PolicyDiff — side-by-side line-level diff for Cedar policy
// updates. Pure UI: takes (before, after) strings and renders three
// states: unchanged · added · removed. No external diff library —
// a minimal LCS-based diff keeps the bundle lean and the algorithm
// auditable.
//
// Used in two places:
//   1. Pre-submit confirmation when an operator edits a Cedar
//      policy in the editor — "review what will change".
//   2. Activity timeline expandable rows when before/after JSON is
//      a Cedar policy field — the row diffs the cedar_policy
//      property and ignores the surrounding wrapper.

import React, { useMemo } from "react";

import { cn } from "@/lib/utils";

type DiffOp = "eq" | "add" | "del";
interface DiffLine {
  op: DiffOp;
  text: string;
  // Stable identity for keys, not displayed.
  id: number;
}

// Compute a line-level diff via LCS. Inputs are arbitrary strings;
// split on \n. Returns the merged-pair representation suitable for
// a unified-style render.
function diffLines(before: string, after: string): DiffLine[] {
  const a = before.split("\n");
  const b = after.split("\n");
  const n = a.length;
  const m = b.length;
  // dp[i][j] = LCS length of a[..i], b[..j]
  const dp: number[][] = [];
  for (let i = 0; i <= n; i++) dp.push(new Array<number>(m + 1).fill(0));
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < m; j++) {
      dp[i + 1][j + 1] =
        a[i] === b[j] ? dp[i][j] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = n;
  let j = m;
  let idSeq = 0;
  while (i > 0 && j > 0) {
    if (a[i - 1] === b[j - 1]) {
      out.unshift({ op: "eq", text: a[i - 1], id: idSeq++ });
      i--;
      j--;
    } else if (dp[i - 1][j] >= dp[i][j - 1]) {
      out.unshift({ op: "del", text: a[i - 1], id: idSeq++ });
      i--;
    } else {
      out.unshift({ op: "add", text: b[j - 1], id: idSeq++ });
      j--;
    }
  }
  while (i > 0) out.unshift({ op: "del", text: a[--i], id: idSeq++ });
  while (j > 0) out.unshift({ op: "add", text: b[--j], id: idSeq++ });
  return out;
}

export interface PolicyDiffProps {
  before: string;
  after: string;
  /** When true, hides unchanged lines beyond ±2 lines of context. */
  collapseContext?: boolean;
  className?: string;
}

export function PolicyDiff({
  before,
  after,
  collapseContext = false,
  className,
}: PolicyDiffProps) {
  const lines = useMemo(() => diffLines(before, after), [before, after]);

  // Stats for the header.
  const adds = lines.filter((l) => l.op === "add").length;
  const dels = lines.filter((l) => l.op === "del").length;

  // Collapse runs of "eq" lines longer than 4 to "± 2 context" if
  // requested. Keeps long-untouched policy chunks from drowning the
  // change.
  const visible: (DiffLine | { gap: number; id: number })[] = [];
  if (collapseContext) {
    let i = 0;
    let idSeq = 100000;
    while (i < lines.length) {
      if (lines[i].op === "eq") {
        // count run length
        let j = i;
        while (j < lines.length && lines[j].op === "eq") j++;
        const runLen = j - i;
        // first/last 2 in the file get full context; in the middle
        // collapse runs > 4
        const headIdx = i === 0 ? 0 : 2;
        const tailIdx = j === lines.length ? runLen : runLen - 2;
        if (runLen > headIdx + (runLen - tailIdx)) {
          for (let k = i; k < i + headIdx; k++) visible.push(lines[k]);
          visible.push({
            gap: tailIdx - headIdx,
            id: idSeq++,
          });
          for (let k = i + tailIdx; k < j; k++) visible.push(lines[k]);
        } else {
          for (let k = i; k < j; k++) visible.push(lines[k]);
        }
        i = j;
      } else {
        visible.push(lines[i]);
        i++;
      }
    }
  } else {
    visible.push(...lines);
  }

  return (
    <div className={cn("space-y-2", className)}>
      <div className="flex items-center gap-3 text-xs">
        <span className="text-emerald-700 dark:text-emerald-400">+{adds}</span>
        <span className="text-destructive">−{dels}</span>
        {dels === 0 && adds === 0 && (
          <span className="text-muted-foreground italic">No changes</span>
        )}
      </div>
      <pre className="overflow-auto rounded border border-border bg-background/60 font-mono text-[11px] leading-snug">
        <code>
          {visible.map((row) => {
            if ("gap" in row) {
              return (
                <div
                  key={row.id}
                  className="select-none border-y border-border/40 bg-muted/30 px-2 py-0.5 text-[10px] text-muted-foreground"
                >
                  … {row.gap} unchanged line{row.gap === 1 ? "" : "s"} …
                </div>
              );
            }
            const cls =
              row.op === "add"
                ? "bg-emerald-500/10 text-emerald-900 dark:text-emerald-200"
                : row.op === "del"
                  ? "bg-destructive/10 text-destructive"
                  : "text-muted-foreground";
            const sign = row.op === "add" ? "+" : row.op === "del" ? "-" : " ";
            return (
              <div key={row.id} className={cn("px-2 py-px", cls)}>
                <span className="mr-2 select-none opacity-60">{sign}</span>
                {row.text || " "}
              </div>
            );
          })}
        </code>
      </pre>
    </div>
  );
}
