import { expect, type Page, type Request } from "@playwright/test";

/**
 * Navigate and wait for the page to go quiet before the test touches it.
 *
 * The console shell refetches its whole inventory on every mount — the scope
 * picker alone pulls every backend and every bucket, which against a
 * long-lived cluster is hundreds of rows. While React renders that, a click on
 * an already-visible, already-enabled control is dropped: the press lands, the
 * handler never runs.
 *
 * That failure mode is invisible in isolation and shows up when a heavyweight
 * file ran just before, so it reads as cross-file interference. It is not —
 * it is how much inventory the page is chewing through at the moment of the
 * click. Waiting for the page to stop fetching removes the guesswork.
 *
 * This used to be `waitForLoadState("networkidle")`. The wait was already
 * wrapped so a page that never settles would not fail here — but its budget
 * was 30s, the same as the whole test timeout, so "never settles" spent the
 * entire test and left nothing for the assertion. Twenty-one specs failed that
 * way, every one of them reporting the assertion rather than the wait.
 */

/**
 * Next.js RSC payloads — `…?_rsc=<hash>`. Link prefetches for every sidebar
 * and breadcrumb destination, fired on mount.
 *
 * They are excluded from the quiet check because two or three of them are
 * reliably still open long after the page is fully rendered and idle, which
 * is what made `networkidle` unreachable on link-dense pages (bucket detail
 * and its tabs, object detail) and reachable everywhere else. Nothing a test
 * asserts on comes from a prefetch: the destination route is not this page.
 *
 * The cost of excluding them is a client-side route transition whose RSC
 * payload is still arriving when the wait returns. That is what Playwright's
 * own auto-waiting on the following assertion is for — and it was already the
 * case, since the old wait gave up too.
 */
const RSC_PAYLOAD = /[?&]_rsc=/;

/**
 * Resolves once nothing but RSC payloads has been in flight for `quietMs`,
 * or after `timeout` regardless — a page that will not go quiet is not a
 * failure here, it just stops being waited for. The budget is small on
 * purpose: whatever the test does next keeps its own wait, so the only thing
 * a long budget buys is a test that dies before it asserts.
 */
async function waitForFetchQuiet(
  page: Page,
  attach: () => Promise<void>,
  { quietMs = 400, timeout = 5_000 } = {},
): Promise<void> {
  let inFlight = 0;
  const counts = (r: Request) => !RSC_PAYLOAD.test(r.url());
  const started = (r: Request) => {
    if (counts(r)) inFlight++;
  };
  const settled = (r: Request) => {
    if (counts(r)) inFlight--;
  };
  page.on("request", started);
  page.on("requestfinished", settled);
  page.on("requestfailed", settled);
  try {
    await attach();
    const deadline = Date.now() + timeout;
    let quietSince: number | null = null;
    while (Date.now() < deadline) {
      if (inFlight > 0) {
        quietSince = null;
      } else {
        quietSince ??= Date.now();
        if (Date.now() - quietSince >= quietMs) return;
      }
      await page.waitForTimeout(50);
    }
  } finally {
    page.off("request", started);
    page.off("requestfinished", settled);
    page.off("requestfailed", settled);
  }
}

export async function gotoSettled(page: Page, url: string): Promise<void> {
  // The listeners are attached before the navigation, not after it, so a fetch
  // the page fires during hydration cannot start unseen and be missed.
  await waitForFetchQuiet(page, async () => {
    await page.goto(url);
  });
}

/**
 * Click a control that only becomes real once the page has settled.
 *
 * Forced on purpose: by this point the element is visible and enabled, and
 * what remains is Playwright's stability heuristic reacting to the shell
 * still painting. Forcing skips that wait, not the visibility one, so a
 * genuinely missing or covered control still fails.
 */
export async function clickWhenSettled(
  page: Page,
  locator: ReturnType<Page["getByRole"]>,
): Promise<void> {
  await expect(locator).toBeVisible({ timeout: 20_000 });
  await waitForFetchQuiet(page, async () => {});
  await locator.click({ force: true });
}
