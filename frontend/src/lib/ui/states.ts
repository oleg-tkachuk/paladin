// The states every interactive control passes through, as one set of
// classes: how it shows focus, how it answers the pointer, how it looks
// pressed or selected. Literal strings, so Tailwind finds them here (as it
// does lib/ui/typography). themeContrast.test.ts reads the surfaces below and
// measures text on them in every palette.

/**
 * The focus indicator: a 2px outline in the ring colour, set off by 2px so
 * it reads against any fill and neither fights a border nor is clipped by
 * one. outline-solid is spelled out: a control's own outline-none sets the
 * style Tailwind's outline-2 reads, and would hide it. The only focus style in
 * the console (states.test.ts).
 */
export const FOCUS_RING =
  "outline-none focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-offset-2 focus-visible:outline-ring";

/** A row or an item answering the pointer. */
export const HOVER_SURFACE = "hover:bg-muted/50";

/** A sidebar row answering the pointer, on the sidebar's own ground. */
export const SIDEBAR_HOVER_SURFACE = "hover:bg-sidebar-accent/60";

/** The current item: the open page in the sidebar, the inspected row. */
export const SELECTED_SURFACE = "bg-primary/10";

/** A control held down: it gives a pixel, unless it opens a menu. */
export const PRESSED = "active:not-aria-[haspopup]:translate-y-px";

/**
 * A field group (input-group) rings itself while the field inside it has
 * focus, with the same outline; the field inside shows none of its own.
 */
export const GROUP_FOCUS_RING =
  "has-[[data-slot=input-group-control]:focus-visible]:outline-2 has-[[data-slot=input-group-control]:focus-visible]:outline-solid has-[[data-slot=input-group-control]:focus-visible]:outline-offset-2 has-[[data-slot=input-group-control]:focus-visible]:outline-ring";
export const IN_GROUP_CONTROL = "focus-visible:outline-none";
