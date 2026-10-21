/**
 * The client an audited call came from, read from the User-Agent it sent.
 *
 * The SDKs send their name and version first (paladin-sdk-go/0.97.0), then
 * the application's own suffix (gateway/1.4), which is how a consumer names
 * itself; the console's server sends paladin-console/<version>. Anything else
 * is shown by its first product, a browser as a browser.
 */

import { CONSOLE_USER_AGENT_PRODUCT } from "@/constants";

/** The names a reader knows Paladin's own clients by. */
const KNOWN_PRODUCTS: Record<string, string> = {
  "paladin-sdk-go": "Go SDK",
  "paladin-sdk-python": "Python SDK",
  [CONSOLE_USER_AGENT_PRODUCT]: "Console",
};

/** Every browser's User-Agent starts with this product. */
const BROWSER_PRODUCT = "Mozilla";
const BROWSER_LABEL = "Browser";

export type ClientKind = "paladin" | "browser" | "other";

export interface AuditClient {
  /** The application when it named itself, else the client: "gateway". */
  name: string;
  /** What carried the call: "Go SDK 0.97.0"; empty when it is the name. */
  via: string;
  kind: ClientKind;
}

// product[/version], the first token of a User-Agent.
const PRODUCT = /^([^/\s]+)(?:\/(\S+))?$/;

function product(token: string): { name: string; version: string } {
  const m = PRODUCT.exec(token);
  return m ? { name: m[1], version: m[2] ?? "" } : { name: token, version: "" };
}

const withVersion = (name: string, version: string) =>
  version ? `${name} ${version}` : name;

/** The client of an entry, or null when its call sent no User-Agent. */
export function parseAuditClient(userAgent: string): AuditClient | null {
  const tokens = userAgent.trim().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return null;
  const first = product(tokens[0]);
  const known = KNOWN_PRODUCTS[first.name];
  if (known) {
    const sdk = withVersion(known, first.version);
    // The suffix the application set: everything after the SDK's product.
    const app = tokens.slice(1).join(" ");
    return app
      ? { name: app, via: sdk, kind: "paladin" }
      : { name: sdk, via: "", kind: "paladin" };
  }
  if (first.name === BROWSER_PRODUCT) {
    return { name: BROWSER_LABEL, via: "", kind: "browser" };
  }
  return {
    name: withVersion(first.name, first.version),
    via: "",
    kind: "other",
  };
}
