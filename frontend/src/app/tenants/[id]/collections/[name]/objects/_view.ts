// Pure view model for the Collection objects page. No React — just the
// saved-view shape (with its runtime schema) and the sort the table shows.
import { z } from "zod/mini";

export interface ViewFilters {
  search?: string;
  /** The status facet's choices; one, as a string, in a view saved before
   *  the facet took several. */
  status?: string | string[];
  tag?: string;
  /** The content-type facet's choices, likewise. */
  type?: string | string[];
  meta?: string;
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
/** A facet's choices in a saved view: several, or the one of an older view. */
const FACET_CHOICE = z.union([z.string(), z.array(z.string())]);

export const SavedViewSchema = z.object({
  name: z.string(),
  // tag / type / meta arrived later; views saved before them parse as they
  // were, with those facets unset. status and type took one choice before
  // they took several, so a view may name either form.
  filters: z.object({
    search: z.optional(z.string()),
    status: z.optional(FACET_CHOICE),
    tag: z.optional(z.string()),
    type: z.optional(FACET_CHOICE),
    meta: z.optional(z.string()),
    recursive: z.optional(z.boolean()),
  }),
});

export type SortDirection = "asc" | "desc" | null;

export interface SortState {
  column: string;
  direction: SortDirection;
}
