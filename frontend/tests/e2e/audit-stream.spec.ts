/**
 * The live audit feed, end to end: browser EventSource → BFF SSE proxy →
 * admin plane → Postgres NOTIFY.
 *
 * This chain had no test of any kind, and it had never delivered a single
 * event. internal/auditstream/hub.go opens `LISTEN paladin_audit` and its own
 * comments credit the notification to a migration trigger — but no migration
 * ever created one, and `paladin_audit` appeared in no SQL at all. The hub
 * listened to a channel nothing wrote to.
 *
 * It stayed invisible because of how it fails. The stream connects, answers
 * `: connected`, and heartbeats forever, so EventSource fires `open` and the
 * /audit page's live badge reads "connected" while nothing ever arrives. A
 * feed that is broken and a feed where nothing is happening look identical.
 *
 * So the assertion that matters is not that the socket opened. It is that an
 * action taken now shows up on the socket — and under the event NAME the
 * console listens for, since `useAuditStream` subscribes to `audit` and a
 * differently-named frame would arrive and be ignored.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedTenant } from "./fixtures/seed";

declare global {
  interface Window {
    __auditFrames?: string[];
    __auditOpen?: boolean;
  }
}

test.describe("Live audit stream", () => {
  test("never hands an event stream to an unauthenticated caller", async ({
    request,
    baseURL,
  }) => {
    // Two layers refuse this, and the assertion is written to survive either.
    // The console's middleware bounces an unauthenticated request to /login
    // (a UX gate, by its own description, not the security boundary), so the
    // route's own 401 is not what a caller sees from outside. What must hold
    // regardless is that nobody without a session is handed a live feed of
    // another tenant's audit log.
    const res = await request.get(`${baseURL}/api/audit/stream`, {
      maxRedirects: 0,
    });
    expect(res.status(), "an anonymous caller was not turned away").not.toBe(
      200,
    );
    expect(res.headers()["content-type"] ?? "").not.toContain(
      "text/event-stream",
    );
  });

  test("delivers a real audit event to the browser", async ({ page }) => {
    await loginAsAdmin(page);

    // Install the same subscription the console uses: an EventSource on the
    // proxy, listening for the `audit` event name that useAuditStream binds.
    await page.evaluate(() => {
      window.__auditFrames = [];
      window.__auditOpen = false;
      const es = new EventSource("/api/audit/stream");
      es.onopen = () => {
        window.__auditOpen = true;
      };
      es.addEventListener("audit", (e) => {
        window.__auditFrames!.push((e as MessageEvent).data);
      });
    });

    // Subscribe BEFORE acting, or the event races the connection and the test
    // passes or fails on timing rather than on behaviour.
    await page.waitForFunction(() => window.__auditOpen === true, null, {
      timeout: 15_000,
    });

    // An admin-plane mutation, audited under the acting admin's tenant —
    // which is the tenant this stream is scoped to. (An anonymous action, a
    // failed login say, carries no tenant and reaches no subscriber by
    // design.)
    await seedTenant({ slugPrefix: "sse" });

    await page.waitForFunction(
      () => (window.__auditFrames?.length ?? 0) > 0,
      null,
      { timeout: 20_000 },
    );

    const frames = await page.evaluate(() => window.__auditFrames!);
    const entry = JSON.parse(frames[0]) as {
      entry_id?: string;
      action?: string;
      actor_tenant_id?: string;
      at?: string;
    };
    // The payload is the hub's compact projection; the console needs the id to
    // fetch the full row and the tenant to have been routed here at all.
    expect(entry.entry_id, `no entry_id in ${frames[0]}`).toBeTruthy();
    expect(entry.action, `no action in ${frames[0]}`).toBeTruthy();
    expect(entry.actor_tenant_id).toBeTruthy();
    expect(Date.parse(entry.at ?? "")).not.toBeNaN();
  });
});
