// How far the topology is zoomed, as a scale where 1 is 100 %. Pure, so the
// buttons, the wheel and "fit" agree on the steps and the bounds.

/** Smallest and largest scale: past these a node's name stops being legible or useful. */
export const ZOOM_MIN = 0.25;
export const ZOOM_MAX = 2;
const ZOOM_STEP = 0.1;

function clampZoom(z: number): number {
  // Rounded to the step's precision, so ten steps in and ten out land back
  // on 100 % rather than 99.99999 %.
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(z * 100) / 100));
}

/** One step in (1) or out (-1). */
export function zoomBy(z: number, direction: 1 | -1): number {
  return clampZoom(z + direction * ZOOM_STEP);
}

/**
 * The scale that shows the whole graph in the viewport, never above 100 %: a
 * small graph is drawn at its own size, not blown up to fill the page.
 */
export function fitZoom(
  content: { width: number; height: number },
  viewport: { width: number; height: number },
): number {
  if (content.width <= 0 || content.height <= 0) return 1;
  if (viewport.width <= 0 || viewport.height <= 0) return 1;
  const fit = Math.min(
    viewport.width / content.width,
    viewport.height / content.height,
    1,
  );
  // Floored to a whole percent, so rounding never leaves a sliver cut off.
  return Math.max(ZOOM_MIN, Math.floor(fit * 100) / 100);
}

/** The readout beside the buttons: "80 %" is written "80%". */
export function zoomPercent(z: number): string {
  return `${Math.round(z * 100)}%`;
}
