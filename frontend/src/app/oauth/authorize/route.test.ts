import { describe, it, expect, vi, beforeEach } from "vitest";

const h = vi.hoisted(() => ({ fetch: vi.fn() }));

vi.mock("@/lib/server/upstream", () => ({
  planeBackendUrls: { iam: "http://iam.test:8085" },
  upstreamFetch: h.fetch,
}));

import { POST } from "./route";
import { OAUTH_AUTHORIZE_PATH } from "@/lib/auth/oauthAuthorize";

const FORM = "client_id=agent&state=st&action=allow&password=secret";
const CLIENT_REDIRECT = "desktop-agent://cb?code=abc&state=st";

function formReq() {
  return new Request(`https://console.test${OAUTH_AUTHORIZE_PATH}`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: FORM,
  });
}

describe("POST /oauth/authorize", () => {
  beforeEach(() => h.fetch.mockReset());

  it("relays the form to the IAM plane without following its redirect", async () => {
    h.fetch.mockResolvedValue(
      new Response(null, {
        status: 302,
        headers: { location: CLIENT_REDIRECT, "set-cookie": "x=y" },
      }),
    );

    const res = await POST(formReq());

    const [url, init] = h.fetch.mock.calls[0];
    expect(url).toBe(`http://iam.test:8085${OAUTH_AUTHORIZE_PATH}`);
    expect(init).toMatchObject({
      method: "POST",
      body: FORM,
      redirect: "manual",
    });
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe(CLIENT_REDIRECT);
    // Only what the browser needs crosses back.
    expect(res.headers.get("set-cookie")).toBeNull();
  });

  it("passes a refusal through as the AS gave it", async () => {
    const body = JSON.stringify({ error: "invalid_client" });
    h.fetch.mockResolvedValue(
      new Response(body, {
        status: 400,
        headers: { "content-type": "application/json" },
      }),
    );

    const res = await POST(formReq());

    expect(res.status).toBe(400);
    expect(res.headers.get("content-type")).toBe("application/json");
    expect(await res.text()).toBe(body);
  });
});
