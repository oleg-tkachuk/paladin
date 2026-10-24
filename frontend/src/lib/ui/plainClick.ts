// A plain click is the page's to handle; one with a modifier or another
// button opens the link where the browser would — a new tab or window.

/** The primary mouse button, as MouseEvent.button reports it. */
const PRIMARY_BUTTON = 0;

/** What isPlainClick reads of a mouse event. */
export type ClickLike = Pick<
  MouseEvent,
  "button" | "metaKey" | "ctrlKey" | "shiftKey" | "altKey"
>;

/** Whether e is a primary-button click with no modifier held. */
export function isPlainClick(e: ClickLike): boolean {
  return (
    e.button === PRIMARY_BUTTON &&
    !e.metaKey &&
    !e.ctrlKey &&
    !e.shiftKey &&
    !e.altKey
  );
}
