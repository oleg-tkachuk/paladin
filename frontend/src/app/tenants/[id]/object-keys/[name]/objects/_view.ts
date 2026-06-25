// Pure view/sort model for the ObjectKey objects page. No React — just the
// saved-view shape (with its runtime schema) and the tri-state sort cycle.
import { z } from "zod";

export interface ViewFilters {
  search?: string;
  status?: string;
  recursive?: boolean;
}

export interface SavedView {
  name: string;
  filters: ViewFilters;
}

// Runtime schema for the localStorage-persisted saved views. localStorage is
// user-editable, so the read is validated (safeParseJson) instead of trusting
// the shape via `as` — a corrupt entry falls back to [] rather than crashing
// the effect with a TypeError on the next `.map`.
export const SavedViewSchema = z.object({
  name: z.string(),
  filters: z.object({
    search: z.string().optional(),
    status: z.string().optional(),
    recursive: z.boolean().optional(),
  }),
});

export type SortDirection = "asc" | "desc" | null;

export interface SortState {
  column: string;
  direction: SortDirection;
}

// Tri-state cycle per column: unsorted → asc → desc → unsorted. Switching to a
// different column starts fresh at asc.
export function getNextSort(prev: SortState, column: string): SortState {
  if (prev.column !== column) return { column, direction: "asc" };
  if (prev.direction === "asc") return { column, direction: "desc" };
  if (prev.direction === "desc") return { column: "", direction: null };
  return { column, direction: "asc" };
}
