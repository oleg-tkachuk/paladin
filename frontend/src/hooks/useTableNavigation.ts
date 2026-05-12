"use client";

// useTableNavigation — j/k/Enter row navigation on lists. Mirrors
// Linear/Notion/cmd-line file managers. The hook is "controlled" —
// caller owns the activeIndex state — so it can also drive multi-
// select (Shift+J/K) and "open in detail pane" affordances.
//
// Behaviour:
//   j / ArrowDown — move down
//   k / ArrowUp   — move up
//   g g           — first row
//   G             — last row
//   Enter         — invoke onOpen on the active row
//   Escape        — clear active
//   /             — focus search input (caller wires search ref)
//
// Ignores keys while the active element is an editable (input,
// textarea, contenteditable) so typing in a search box doesn't move
// the cursor.

import { useEffect, useRef } from "react";

export interface UseTableNavigationOptions {
  rowCount: number;
  activeIndex: number;
  onActiveIndexChange: (next: number) => void;
  onOpen?: (index: number) => void;
  /** Optional focusable ref for the page's search input — focused on `/`. */
  searchRef?: React.RefObject<HTMLInputElement | null>;
  /** Disable handlers entirely. Pass true when a modal is open. */
  disabled?: boolean;
}

function isEditableTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (el.isContentEditable) return true;
  return false;
}

export function useTableNavigation({
  rowCount,
  activeIndex,
  onActiveIndexChange,
  onOpen,
  searchRef,
  disabled,
}: UseTableNavigationOptions) {
  // Track a pending 'g' for "gg → first row".
  const gPending = useRef(false);

  useEffect(() => {
    if (disabled || rowCount === 0) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isEditableTarget(e.target)) return;
      switch (e.key) {
        case "j":
        case "ArrowDown":
          e.preventDefault();
          onActiveIndexChange(Math.min(rowCount - 1, activeIndex + 1));
          gPending.current = false;
          break;
        case "k":
        case "ArrowUp":
          e.preventDefault();
          onActiveIndexChange(Math.max(0, activeIndex - 1));
          gPending.current = false;
          break;
        case "g":
          if (gPending.current) {
            e.preventDefault();
            onActiveIndexChange(0);
            gPending.current = false;
          } else {
            gPending.current = true;
            // Decay if no follow-up within 800ms.
            setTimeout(() => {
              gPending.current = false;
            }, 800);
          }
          break;
        case "G":
          if (e.shiftKey) {
            e.preventDefault();
            onActiveIndexChange(rowCount - 1);
          }
          gPending.current = false;
          break;
        case "Enter":
          if (onOpen && activeIndex >= 0) {
            e.preventDefault();
            onOpen(activeIndex);
          }
          gPending.current = false;
          break;
        case "Escape":
          onActiveIndexChange(-1);
          gPending.current = false;
          break;
        case "/":
          if (searchRef?.current) {
            e.preventDefault();
            searchRef.current.focus();
            searchRef.current.select?.();
          }
          gPending.current = false;
          break;
        default:
          if (e.key !== "g") gPending.current = false;
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [activeIndex, rowCount, onActiveIndexChange, onOpen, searchRef, disabled]);
}
