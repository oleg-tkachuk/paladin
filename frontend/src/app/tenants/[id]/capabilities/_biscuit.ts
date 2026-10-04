// A compact JWT has dots between its parts; a Biscuit, being one base64url
// string, has none. The server makes the same distinction.
export const isJWT = (token: string) => token.includes(".");

// How many leading bytes of a block's revocation id the console shows: enough
// to tell the blocks of one copy apart, short enough for a table cell.
export const REVOCATION_ID_SHOWN_BYTES = 6;

export function shortRevocationId(id: Uint8Array): string {
  return Array.from(id.slice(0, REVOCATION_ID_SHOWN_BYTES), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
}
