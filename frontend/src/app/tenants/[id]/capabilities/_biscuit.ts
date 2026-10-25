import { BiscuitStatus } from "@/gen/paladin/admin/v1/capability_service_pb";

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

/**
 * Why an issued capability came without its Biscuit, as the reveal panel
 * says it; null when the Biscuit is there. A key-bound capability is proved
 * by its holder, who presents the token; one valid only later is sealed when
 * it starts. A server too old to say leaves the status unspecified.
 */
export function missingBiscuitReason(status: BiscuitStatus): string | null {
  switch (status) {
    case BiscuitStatus.ISSUED:
      return null;
    case BiscuitStatus.KEY_BOUND:
      return "No Biscuit: the capability is bound to a key, so its holder presents the token and proves the key.";
    case BiscuitStatus.NOT_YET_VALID:
      return "No Biscuit: the capability is valid only from its not-before time.";
    default:
      return "No Biscuit came with this capability.";
  }
}
