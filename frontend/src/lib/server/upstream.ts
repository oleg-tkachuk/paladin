import { Agent, fetch as undiciFetch } from "undici";

// The console server's one outbound leg: the OAuth consent form's relay to
// the iam plane (app/oauth/authorize). Every other call the console makes goes
// from the browser through the session role (ADR-0048).
export const iamBackendUrl =
  process.env.PALADIN_IAM_URL || "http://paladin-core-iam:8085";

// Bounded connection pool for the outbound leg.
//
// undici's default agent leaves connections-per-origin unlimited, so a page
// that fans out — /mcp issues eight RPCs across the admin and iam planes at
// once — opens that many TLS connections simultaneously on a cold pool. Each
// handshake is CPU work on Node's single thread, and the planes give a
// handshake five seconds (Go derives the TLS deadline from ReadHeaderTimeout).
// Two missed it during an e2e run: the plane logged "TLS handshake error ...
// read tcp: i/o timeout" from this pod's IP, the GetHealth behind it surfaced
// as "500 fetch failed", and since the token exchange rides the same plane the
// browser's 401 self-heal had nothing to recover with — so the rest of that
// page answered 401.
//
// A cap makes a burst queue instead of stampede; keep-alive means the next
// page reuses what this one opened rather than handshaking again. Sized above
// the widest fan-out we have so one page still runs concurrently.
//
// The dispatcher is passed explicitly rather than installed with
// setGlobalDispatcher: that relies on a versioned well-known symbol shared
// with Node's built-in fetch, and a mismatch would silently do nothing.
const upstreamAgent = new Agent({
  connections: Number(process.env.PALADIN_BFF_MAX_CONNECTIONS || 16),
  // Long enough to be reused across a page's requests and the next
  // navigation; short enough not to hold sockets to a pod that has rolled.
  keepAliveTimeout: 30_000,
  keepAliveMaxTimeout: 60_000,
  // Fail a stuck connect rather than occupying a pool slot until the request
  // itself times out.
  connect: { timeout: 10_000 },
});

/**
 * fetch over the shared pool. undici's own fetch rather than Node's global
 * one: an Agent only works with the fetch of the same undici major, and
 * Node's bundled undici follows the Node release — Node 24 ships undici 7,
 * which rejects this undici 8 Agent and fails every request with "fetch
 * failed". The casts bridge undici's WHATWG types and the DOM's.
 */
export const upstreamFetch: typeof fetch = (input, init) =>
  undiciFetch(
    input as Parameters<typeof undiciFetch>[0],
    { ...init, dispatcher: upstreamAgent } as Parameters<typeof undiciFetch>[1],
  ) as unknown as Promise<Response>;
