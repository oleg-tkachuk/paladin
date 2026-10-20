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

/**
 * A table row the row keys are on (useTableKeyboard): the same outline,
 * drawn inside the row's edge, since the table's scrolling container would
 * clip one set outside it, over the hover surface.
 */
export const ROW_FOCUS_RING =
  "outline-none focus-visible:outline-2 focus-visible:outline-solid focus-visible:-outline-offset-2 focus-visible:outline-ring focus-visible:bg-muted/50";

/** A row or an item answering the pointer. */
export const HOVER_SURFACE = "hover:bg-muted/50";

/** A sidebar row answering the pointer, on the sidebar's own ground. */
export const SIDEBAR_HOVER_SURFACE = "hover:bg-sidebar-accent/60";

/** The current item: the open page in the sidebar, the inspected row. */
export const SELECTED_SURFACE = "bg-primary/10";

/** The open page in the sidebar: the accent's tint filled in, a step
 *  stronger than a selected row, on the sidebar's deeper ground. */
export const SIDEBAR_SELECTED_SURFACE = "bg-primary/15";

/** A control held down: it gives a pixel, unless it opens a menu. */
export const PRESSED = "active:not-aria-[haspopup]:translate-y-px";

/**
 * A field group (input-group) rings itself while the field inside it has
 * focus, with the same outline; the field inside shows none of its own.
 */
export const GROUP_FOCUS_RING =
  "has-[[data-slot=input-group-control]:focus-visible]:outline-2 has-[[data-slot=input-group-control]:focus-visible]:outline-solid has-[[data-slot=input-group-control]:focus-visible]:outline-offset-2 has-[[data-slot=input-group-control]:focus-visible]:outline-ring";
export const IN_GROUP_CONTROL = "focus-visible:outline-none";

/**
 * A bar that stays in view as the page scrolls: it sticks just under the
 * shell's top bar (TopBar is h-14; states.test holds the two together).
 */
export const STICKY_BELOW_TOPBAR = "sticky top-14 z-20";

/** Frosted glass for what scrolls over content: the page shows through, blurred. */
export const FROSTED = "bg-background/80 backdrop-blur-md";

/** The same on a muted band, as a section's own header. */
export const FROSTED_MUTED = "bg-muted/70 backdrop-blur-md";

/** The chosen option card of a set, the selected entry of a list. */
export const ACTIVE_SURFACE = "border-active";

/** An option card, active while the checkbox or radio inside it is checked. */
export const CHECKED_OPTION = "has-[[data-state=checked]]:border-active";

/** border-active over a dialog's surface rather than a card's. */
export const ACTIVE_ON_POPOVER = "[--active-ground:var(--popover)]";
