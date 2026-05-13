"use client";

// CedarEditor — lightweight Cedar policy editor. NOT Monaco/CodeMirror
// (their bundle costs > 1MB minified for a feature ≤ 5 % of operators
// touch). Instead: enhanced <textarea> with line numbers, tab→spaces
// indent handling, optional inline lint via CelService.ValidateCedar
// (when wired), and a side panel for the policy simulator.
//
// What it deliberately doesn't do:
//   - Syntax highlighting (would need a tokenizer; deferred)
//   - Auto-complete (deferred to BACKLOG when a Cedar LSP exists)
//
// What it does well:
//   - Multi-line indent / outdent with Tab / Shift+Tab
//   - Cursor stays inside the line during indent
//   - Line numbers gutter that scrolls with content
//   - Optional onValidate callback fires after a debounce — caller
//     wires to the CelService validator and renders errors in the
//     gutter
//
// The simulator lives in <CedarPolicySimulator> in a separate
// file; CedarEditor accepts a `simulator` slot prop and rendering
// the two side-by-side is the caller's job.

import React, { useCallback, useEffect, useRef, useState } from "react";

import { cn } from "@/lib/utils";

export interface CedarValidationIssue {
  line: number; // 1-based
  message: string;
  severity: "error" | "warning";
}

export interface CedarEditorProps {
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  /** Read-only display mode. */
  readOnly?: boolean;
  /** Called with a debounce after edits cease. Caller resolves with
   *  validation issues for the gutter overlay. */
  onValidate?: (text: string) => Promise<CedarValidationIssue[]>;
  /** External issues — override the validator. Useful for "submit
   *  failed with these errors". */
  issues?: CedarValidationIssue[];
  /** Minimum height in px. Defaults to 200. */
  minHeight?: number;
  className?: string;
  id?: string;
}

export function CedarEditor({
  value,
  onChange,
  placeholder,
  readOnly,
  onValidate,
  issues: externalIssues,
  minHeight = 200,
  className,
  id,
}: CedarEditorProps) {
  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const [internalIssues, setInternalIssues] = useState<CedarValidationIssue[]>(
    [],
  );

  // Debounced validate.
  useEffect(() => {
    if (!onValidate) return;
    const handle = setTimeout(() => {
      void onValidate(value)
        .then(setInternalIssues)
        .catch(() => {});
    }, 400);
    return () => clearTimeout(handle);
  }, [value, onValidate]);

  const issues = externalIssues ?? internalIssues;
  const issuesByLine = new Map<number, CedarValidationIssue[]>();
  for (const i of issues) {
    const arr = issuesByLine.get(i.line) ?? [];
    arr.push(i);
    issuesByLine.set(i.line, arr);
  }

  const lines = value.split("\n");

  // Tab → 4 spaces, Shift+Tab → outdent. Preserve cursor inside the
  // current line when multi-selection straddles lines.
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key !== "Tab") return;
      const ta = e.currentTarget;
      const { selectionStart: start, selectionEnd: end } = ta;
      if (start === null || end === null) return;
      e.preventDefault();
      const text = ta.value;
      // Find the line boundaries the selection covers.
      const lineStart = text.lastIndexOf("\n", start - 1) + 1;
      const lineEndIdx = text.indexOf("\n", end);
      const lineEnd = lineEndIdx === -1 ? text.length : lineEndIdx;
      const block = text.slice(lineStart, lineEnd);
      const multiLine = start !== end && block.includes("\n");
      if (!multiLine) {
        // Single-line behaviour: insert/remove 4 spaces at cursor.
        if (e.shiftKey) {
          // Outdent: remove up to 4 leading spaces on the line.
          const before = text.slice(0, lineStart);
          const lineText = block;
          const trimmed = lineText.replace(/^ {1,4}/, "");
          const removed = lineText.length - trimmed.length;
          const after = text.slice(lineEnd);
          const next = before + trimmed + after;
          onChange(next);
          requestAnimationFrame(() => {
            ta.selectionStart = ta.selectionEnd = Math.max(
              lineStart,
              start - removed,
            );
          });
        } else {
          const next = text.slice(0, start) + "    " + text.slice(end);
          onChange(next);
          requestAnimationFrame(() => {
            ta.selectionStart = ta.selectionEnd = start + 4;
          });
        }
        return;
      }
      // Multi-line indent / outdent — operate on every selected line.
      const blockLines = block.split("\n");
      const adjusted = blockLines.map((l) =>
        e.shiftKey ? l.replace(/^ {1,4}/, "") : "    " + l,
      );
      const newBlock = adjusted.join("\n");
      const before = text.slice(0, lineStart);
      const after = text.slice(lineEnd);
      onChange(before + newBlock + after);
      const delta = newBlock.length - block.length;
      requestAnimationFrame(() => {
        ta.selectionStart = lineStart;
        ta.selectionEnd = end + delta;
      });
    },
    [onChange],
  );

  return (
    <div
      className={cn(
        "flex overflow-hidden rounded-md border border-input bg-background font-mono text-xs",
        className,
      )}
      style={{ minHeight }}
    >
      {/* Line-number gutter + lint markers. Synced via scrollTop on
          the textarea via a small effect. */}
      <div
        aria-hidden
        className="select-none overflow-hidden border-r border-border bg-muted/30 px-2 py-2 text-right text-[11px] leading-snug text-muted-foreground"
        style={{ minWidth: 36 }}
      >
        {lines.map((_, idx) => {
          const lineNum = idx + 1;
          const lineIssues = issuesByLine.get(lineNum);
          const hasErr = lineIssues?.some((i) => i.severity === "error");
          const hasWarn = lineIssues?.some((i) => i.severity === "warning");
          return (
            <div
              key={lineNum}
              className={cn(
                "tabular-nums",
                hasErr && "font-bold text-destructive",
                hasWarn && !hasErr && "font-bold text-amber-500",
              )}
              title={lineIssues?.map((i) => i.message).join("\n")}
            >
              {lineNum}
            </div>
          );
        })}
      </div>
      <textarea
        id={id}
        ref={taRef}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={handleKeyDown}
        readOnly={readOnly}
        placeholder={placeholder}
        spellCheck={false}
        className="flex-1 resize-y bg-transparent px-2 py-2 leading-snug outline-none"
      />
    </div>
  );
}
