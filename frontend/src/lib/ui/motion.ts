// When the console moves: a page fading into the next on navigation, and a
// state's colour easing into the next. Both only where the browser can and
// the user has not asked for less motion. How long each takes is a token in
// globals.css (--motion-*), read by the CSS that animates; nothing here
// names a duration.

/** The media query a user who asked for less motion matches. */
export const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

/**
 * The view-transition class a page change runs under; globals.css times
 * ::view-transition-old(.page-change) and its new side by it.
 */
export const PAGE_TRANSITION_CLASS = "page-change";

/** Whether the document can run a view transition. */
export function supportsViewTransitions(
  doc: Pick<Document, "startViewTransition"> | undefined,
): boolean {
  return typeof doc?.startViewTransition === "function";
}

/** Whether the user asked for less motion. */
export function prefersReducedMotion(
  win: Pick<Window, "matchMedia"> | undefined,
): boolean {
  return win?.matchMedia?.(REDUCED_MOTION_QUERY).matches ?? false;
}

/**
 * Whether a page change animates: the browser runs view transitions and
 * the user has not asked for less motion. Neither known — a render with no
 * document — is no.
 */
export function pageTransitionsOn(
  doc: Pick<Document, "startViewTransition"> | undefined,
  win: Pick<Window, "matchMedia"> | undefined,
): boolean {
  return supportsViewTransitions(doc) && !prefersReducedMotion(win);
}
