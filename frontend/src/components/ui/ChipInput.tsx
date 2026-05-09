"use client";

import * as React from "react";
import { XMarkIcon } from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";

// ChipInput — multi-value text input rendered as chips.
//
// Used for fields where the API expects a list of short strings
// (resource prefixes, CIDR ranges, audience overrides). The legacy
// "comma-separated string" pattern was confusing: users typed values
// without seeing what would actually be parsed, typos surfaced only on
// submit, and the stored value didn't match the field shape.
//
// Behaviour:
//   - Type text → press Enter / comma / space → text becomes a chip,
//     input clears for the next entry.
//   - Click the × on a chip → removes it.
//   - Backspace on empty input → removes the last chip (familiar from
//     email To/Cc fields, GitHub label pickers, Linear assignees).
//   - Paste splits on whitespace / comma → all tokens land as chips at
//     once, so paste-from-spreadsheet-row "just works".
//   - Duplicates are silently dropped (case-sensitive equality on the
//     trimmed value) — the parent gets a deduped list.
//
// Looks like an `<Input>`: same border, same focus ring, same height
// floor. Chips inline at the front, the typing slot grows to fill the
// remaining space.
//
// Generic over the chip-validation strategy is BACKLOG — for now the
// parent can run validation against `values` after the change. Adding
// per-token validation here would force every caller to thread an
// error message component, which most call sites don't need.

interface ChipInputProps {
  values: string[];
  onChange: (next: string[]) => void;
  /** id forwarded to the inner <input> so an outer <Label htmlFor> binds. */
  id?: string;
  placeholder?: string;
  /** Disables typing and chip removal. Useful while the form is submitting. */
  disabled?: boolean;
  /** Forwarded to the wrapper className for one-off overrides. */
  className?: string;
}

// Tokens are committed when the user types a separator (Enter, comma,
// space). Tab is intentionally omitted so users can keep tabbing
// through form fields without their half-typed value being captured.
const COMMIT_KEYS = new Set([",", " ", "Enter"]);

export function ChipInput({
  values,
  onChange,
  id,
  placeholder,
  disabled,
  className,
}: ChipInputProps) {
  const [draft, setDraft] = React.useState("");
  const inputRef = React.useRef<HTMLInputElement>(null);

  // commit pushes one or more trimmed, non-empty, non-duplicate tokens
  // onto `values`. Centralised so Enter, comma, space, blur, and paste
  // all share the same dedup + filter rules.
  const commit = React.useCallback(
    (raw: string) => {
      const tokens = raw
        .split(/[\s,]+/)
        .map((t) => t.trim())
        .filter(Boolean);
      if (tokens.length === 0) return;
      const seen = new Set(values);
      const additions: string[] = [];
      for (const t of tokens) {
        if (!seen.has(t)) {
          seen.add(t);
          additions.push(t);
        }
      }
      if (additions.length > 0) {
        onChange([...values, ...additions]);
      }
      setDraft("");
    },
    [values, onChange],
  );

  const removeAt = (idx: number) => {
    const next = values.slice();
    next.splice(idx, 1);
    onChange(next);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (COMMIT_KEYS.has(e.key)) {
      // Space inside an empty draft shouldn't fire — let it through so
      // users can clear the field by spamming space if they want.
      if (e.key === " " && draft.length === 0) return;
      e.preventDefault();
      commit(draft);
      return;
    }
    if (e.key === "Backspace" && draft.length === 0 && values.length > 0) {
      e.preventDefault();
      removeAt(values.length - 1);
    }
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLInputElement>) => {
    const pasted = e.clipboardData.getData("text");
    // Only intercept if the paste actually contains a separator —
    // otherwise let the native paste happen so single-token pastes
    // sit in the draft input, ready for a manual commit.
    if (!/[\s,]/.test(pasted)) return;
    e.preventDefault();
    commit(draft + pasted);
  };

  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-md border border-input bg-background px-2 py-1.5 text-sm",
        "focus-within:outline-none focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-0",
        disabled && "cursor-not-allowed opacity-60",
        className,
      )}
      onClick={() => inputRef.current?.focus()}
    >
      {values.map((v, i) => (
        <span
          key={`${v}-${i}`}
          className="inline-flex items-center gap-1 rounded-md bg-secondary px-1.5 py-0.5 font-mono text-xs"
        >
          {v}
          <button
            type="button"
            onClick={(e) => {
              // stopPropagation prevents the wrapper's onClick from
              // re-focusing the input mid-removal, which would steal
              // the click on adjacent chips.
              e.stopPropagation();
              if (!disabled) removeAt(i);
            }}
            className="text-muted-foreground hover:text-foreground"
            aria-label={`Remove ${v}`}
            tabIndex={-1}
            disabled={disabled}
          >
            <XMarkIcon className="size-3" />
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        id={id}
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        onBlur={() => commit(draft)}
        placeholder={values.length === 0 ? placeholder : undefined}
        disabled={disabled}
        className="flex-1 min-w-[8ch] bg-transparent font-mono text-xs outline-none placeholder:text-muted-foreground/70 disabled:cursor-not-allowed"
      />
    </div>
  );
}
