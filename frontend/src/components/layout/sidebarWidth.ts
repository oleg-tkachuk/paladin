// The desktop sidebar's widths, and the matching left offsets for anything
// fixed to the viewport that has to clear it. Each pair must agree, so both
// live here; the classes are written out whole for Tailwind to find them.

/** The sidebar's width, expanded and collapsed. */
export const SIDEBAR_WIDTH = {
  expanded: "w-64",
  collapsed: "w-17",
} as const;

/** The attribute the sidebar carries while collapsed. */
export const SIDEBAR_COLLAPSED_ATTR = "data-sidebar-collapsed";

/**
 * Left offset, from the lg breakpoint where the sidebar shows, that clears
 * it: its expanded width, or its collapsed one while the page holds a
 * collapsed sidebar.
 */
export const CLEAR_SIDEBAR =
  "lg:left-64 lg:[:root:has([data-sidebar-collapsed])_&]:left-17";
