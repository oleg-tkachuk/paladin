"use client";

import React from "react";
import {
  ChevronUpIcon,
  ChevronDownIcon,
  ChevronUpDownIcon,
} from "@heroicons/react/24/outline";
import { cn } from "@/lib/utils";

export type SortDirection = "asc" | "desc" | null;

export interface SortState {
  column: string;
  direction: SortDirection;
}

interface SortableHeaderProps {
  label: string;
  column: string;
  currentSort: SortState;
  onSort: (column: string) => void;
  className?: string;
}

export function SortableHeader({
  label,
  column,
  currentSort,
  onSort,
  className,
}: SortableHeaderProps) {
  const isActive = currentSort.column === column;
  const direction = isActive ? currentSort.direction : null;

  return (
    <button
      onClick={() => onSort(column)}
      className={cn(
        "group inline-flex items-center gap-1.5 text-xs font-bold uppercase tracking-wider transition-colors",
        isActive ? "text-indigo-400" : "text-slate-500 hover:text-slate-300",
        className,
      )}
    >
      {label}
      <span className="flex-shrink-0">
        {direction === "asc" ? (
          <ChevronUpIcon className="w-3.5 h-3.5" />
        ) : direction === "desc" ? (
          <ChevronDownIcon className="w-3.5 h-3.5" />
        ) : (
          <ChevronUpDownIcon className="w-3.5 h-3.5 opacity-0 group-hover:opacity-60 transition-opacity" />
        )}
      </span>
    </button>
  );
}

/**
 * Cycles sort direction: null → asc → desc → null
 */
export function nextSortDirection(current: SortDirection): SortDirection {
  if (current === null) return "asc";
  if (current === "asc") return "desc";
  return null;
}

/**
 * Returns the next sort state for a column click.
 */
export function getNextSort(current: SortState, column: string): SortState {
  if (current.column === column) {
    const next = nextSortDirection(current.direction);
    return next ? { column, direction: next } : { column: "", direction: null };
  }
  return { column, direction: "asc" };
}
