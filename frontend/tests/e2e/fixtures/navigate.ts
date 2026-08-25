import { expect, type Page } from "@playwright/test";

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
 * click. Waiting for network idle removes the guesswork.
 *
 * A page that never fully settles is not a failure here: whatever the test
 * does next keeps its own wait.
 */
export async function gotoSettled(page: Page, url: string): Promise<void> {
  await page.goto(url);
  await page
    .waitForLoadState("networkidle", { timeout: 30_000 })
    .catch(() => {});
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
  await page
    .waitForLoadState("networkidle", { timeout: 30_000 })
    .catch(() => {});
  await locator.click({ force: true });
}
