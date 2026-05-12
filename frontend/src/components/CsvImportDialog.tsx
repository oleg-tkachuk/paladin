"use client";

// CsvImportDialog — paste-in or file-pick CSV → preview → bulk-create.
// Generic so it can drive tenant / bucket / OK imports; caller
// supplies (1) the columns + their mapping to a create-request and
// (2) the per-row submit function.
//
// Why CSV specifically (not JSON or YAML): operators bring CSV
// from spreadsheets. We accept comma OR tab delimiters; first row
// is the header. Rows with parse errors are flagged in preview but
// don't block import — the operator can skip them or fix and
// re-paste. Successful + failed counts surface at the end.

import React, { useMemo, useState } from "react";
import {
  ArrowUpTrayIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  TableCellsIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export interface CsvColumn {
  /** Header column key (the CSV first-row cell). */
  key: string;
  /** Human label shown in the preview header. */
  label: string;
  required?: boolean;
}

export interface CsvImportDialogProps<T> {
  open: boolean;
  onOpenChange: (next: boolean) => void;
  title: string;
  description?: string;
  columns: CsvColumn[];
  /** Parse a header-keyed row record into a typed value, or throw a
   *  user-readable error to skip the row. */
  parseRow: (row: Record<string, string>) => T;
  /** Submit one parsed row. Throw to record a failure for that row. */
  submitRow: (item: T) => Promise<void>;
  /** Optional placeholder for the textarea — sample CSV. */
  placeholder?: string;
}

interface ParsedRow<T> {
  raw: Record<string, string>;
  ok: boolean;
  value?: T;
  error?: string;
}

function detectDelimiter(line: string): string {
  return line.includes("\t") ? "\t" : ",";
}

function parseCSV(text: string): Record<string, string>[] {
  // Minimal RFC-4180-ish CSV: respects double-quoted cells (which
  // may contain the delimiter / newlines). Doesn't claim full
  // compliance (no Unicode-aware splits, no BOM stripping for non-
  // ASCII headers). Operators bringing edge-case CSV from Excel
  // can use the file-picker variant where the browser File API
  // gives us a UTF-8 string already.
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  while (lines.length && lines[lines.length - 1].trim() === "") lines.pop();
  if (lines.length === 0) return [];
  const delim = detectDelimiter(lines[0]);
  const splitRow = (line: string): string[] => {
    const out: string[] = [];
    let buf = "";
    let inQ = false;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (inQ) {
        if (c === '"' && line[i + 1] === '"') {
          buf += '"';
          i++;
        } else if (c === '"') {
          inQ = false;
        } else {
          buf += c;
        }
      } else if (c === '"') {
        inQ = true;
      } else if (c === delim) {
        out.push(buf);
        buf = "";
      } else {
        buf += c;
      }
    }
    out.push(buf);
    return out;
  };
  const header = splitRow(lines[0]).map((h) => h.trim());
  const rows: Record<string, string>[] = [];
  for (let i = 1; i < lines.length; i++) {
    const cells = splitRow(lines[i]);
    const obj: Record<string, string> = {};
    for (let j = 0; j < header.length; j++) {
      obj[header[j]] = (cells[j] ?? "").trim();
    }
    rows.push(obj);
  }
  return rows;
}

export function CsvImportDialog<T>({
  open,
  onOpenChange,
  title,
  description,
  columns,
  parseRow,
  submitRow,
  placeholder,
}: CsvImportDialogProps<T>) {
  const [text, setText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [results, setResults] = useState<{
    successes: number;
    failures: { row: number; reason: string }[];
  } | null>(null);

  const parsed = useMemo<ParsedRow<T>[]>(() => {
    if (!text.trim()) return [];
    const rows = parseCSV(text);
    return rows.map((raw) => {
      try {
        return { raw, ok: true, value: parseRow(raw) };
      } catch (err) {
        return {
          raw,
          ok: false,
          error: err instanceof Error ? err.message : String(err),
        };
      }
    });
  }, [text, parseRow]);

  const validCount = parsed.filter((p) => p.ok).length;
  const invalidCount = parsed.length - validCount;

  const pickFile = () => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept =
      ".csv,.tsv,.txt,text/csv,text/tab-separated-values,text/plain";
    input.onchange = async () => {
      const f = input.files?.[0];
      if (!f) return;
      const t = await f.text();
      setText(t);
    };
    input.click();
  };

  const handleImport = async () => {
    setSubmitting(true);
    let successes = 0;
    const failures: { row: number; reason: string }[] = [];
    for (let i = 0; i < parsed.length; i++) {
      const p = parsed[i];
      if (!p.ok || !p.value) {
        failures.push({ row: i + 2, reason: p.error || "parse error" });
        continue;
      }
      try {
        await submitRow(p.value);
        successes++;
      } catch (err) {
        failures.push({
          row: i + 2,
          reason: err instanceof Error ? err.message : String(err),
        });
      }
    }
    setResults({ successes, failures });
    setSubmitting(false);
  };

  const handleReset = () => {
    setText("");
    setResults(null);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>

        {results ? (
          <ResultsView results={results} parsedCount={parsed.length} />
        ) : (
          <div className="space-y-4 py-2">
            <div className="flex items-center justify-between">
              <p className="text-xs text-muted-foreground">
                First row is the header. Columns:{" "}
                {columns.map((c, i) => (
                  <React.Fragment key={c.key}>
                    {i > 0 && ", "}
                    <span className={cn(T.code, "text-[10px]")}>{c.key}</span>
                    {c.required && <span className="text-destructive">*</span>}
                  </React.Fragment>
                ))}
              </p>
              <Button size="sm" variant="outline" onClick={pickFile}>
                <ArrowUpTrayIcon className="size-4" />
                Pick file
              </Button>
            </div>
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={placeholder || "Paste CSV here…"}
              className="h-40 w-full resize-y rounded-md border border-input bg-background p-2 font-mono text-xs leading-relaxed"
            />
            {parsed.length > 0 && (
              <Card className="overflow-auto p-0">
                <table className="w-full text-xs">
                  <thead className="bg-muted/40 text-[10px] uppercase tracking-wider text-muted-foreground">
                    <tr>
                      <th className="px-2 py-1 text-left">#</th>
                      {columns.map((c) => (
                        <th key={c.key} className="px-2 py-1 text-left">
                          {c.label}
                        </th>
                      ))}
                      <th className="px-2 py-1 text-left">Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {parsed.slice(0, 50).map((p, i) => (
                      <tr
                        key={i}
                        className={cn(
                          "border-t border-border",
                          !p.ok && "bg-destructive/5",
                        )}
                      >
                        <td className="px-2 py-1 font-mono text-muted-foreground">
                          {i + 2}
                        </td>
                        {columns.map((c) => (
                          <td
                            key={c.key}
                            className="px-2 py-1 font-mono text-[11px]"
                          >
                            {p.raw[c.key] || (
                              <span className="text-muted-foreground italic">
                                —
                              </span>
                            )}
                          </td>
                        ))}
                        <td className="px-2 py-1">
                          {p.ok ? (
                            <Badge variant="outline" className={T.labelTight}>
                              ready
                            </Badge>
                          ) : (
                            <span
                              className="text-[10px] text-destructive"
                              title={p.error}
                            >
                              {p.error?.slice(0, 40) || "parse error"}
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {parsed.length > 50 && (
                  <div className="border-t border-border px-2 py-1 text-center text-[10px] text-muted-foreground">
                    + {parsed.length - 50} more rows (preview truncated)
                  </div>
                )}
              </Card>
            )}
            {parsed.length > 0 && (
              <p className="text-xs text-muted-foreground">
                <span className="font-medium text-foreground">
                  {validCount}
                </span>{" "}
                ready ·{" "}
                <span className={cn(invalidCount > 0 && "text-destructive")}>
                  {invalidCount} parse-failed
                </span>
              </p>
            )}
          </div>
        )}

        <DialogFooter>
          {results ? (
            <>
              <Button variant="ghost" onClick={handleReset}>
                Import more
              </Button>
              <Button onClick={() => onOpenChange(false)}>Close</Button>
            </>
          ) : (
            <>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button
                onClick={handleImport}
                disabled={submitting || validCount === 0}
              >
                {submitting ? "Importing…" : `Import ${validCount}`}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ResultsView({
  results,
  parsedCount,
}: {
  results: { successes: number; failures: { row: number; reason: string }[] };
  parsedCount: number;
}) {
  return (
    <div className="space-y-3 py-2">
      <div className="grid grid-cols-3 gap-2 text-center text-xs">
        <Card className="border-emerald-500/40 bg-emerald-500/5 p-3">
          <CheckCircleIcon className="mx-auto size-5 text-emerald-600" />
          <p className="mt-1 text-lg font-semibold tabular-nums text-emerald-700 dark:text-emerald-300">
            {results.successes}
          </p>
          <p className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Imported
          </p>
        </Card>
        <Card className="border-destructive/40 bg-destructive/5 p-3">
          <ExclamationTriangleIcon className="mx-auto size-5 text-destructive" />
          <p className="mt-1 text-lg font-semibold tabular-nums text-destructive">
            {results.failures.length}
          </p>
          <p className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Failed
          </p>
        </Card>
        <Card className="p-3">
          <TableCellsIcon className="mx-auto size-5 text-muted-foreground" />
          <p className="mt-1 text-lg font-semibold tabular-nums">
            {parsedCount}
          </p>
          <p className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Total
          </p>
        </Card>
      </div>
      {results.failures.length > 0 && (
        <Card className="max-h-48 overflow-auto p-3 text-xs">
          <p className="mb-2 font-medium">Failures</p>
          <ul className="space-y-1">
            {results.failures.map((f) => (
              <li key={f.row} className="font-mono text-[10px]">
                <span className="text-muted-foreground">row {f.row}:</span>{" "}
                <span className="text-destructive">{f.reason}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}
