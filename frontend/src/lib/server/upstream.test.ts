// @vitest-environment node
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { upstreamFetch } from "./upstream";

const BODY = "pong";

describe("upstreamFetch", () => {
  let server: Server;
  let url: string;

  beforeAll(async () => {
    server = createServer((req, res) => {
      res.setHeader("content-type", "text/plain");
      res.end(`${req.method} ${BODY}`);
    });
    await new Promise<void>((resolve) =>
      server.listen(0, "127.0.0.1", resolve),
    );
    url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/`;
  });

  afterAll(() => new Promise<void>((resolve) => server.close(() => resolve())));

  // The pooled Agent must be usable by the fetch it is handed to, whatever
  // undici the running Node bundles.
  it("reaches the upstream through the shared pool", async () => {
    const res = await upstreamFetch(url, { method: "POST", body: "x" });
    expect(res.status).toBe(200);
    expect(await res.text()).toBe(`POST ${BODY}`);
  });
});
