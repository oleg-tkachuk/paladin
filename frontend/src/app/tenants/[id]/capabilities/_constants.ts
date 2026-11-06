import type { Capability } from "@/gen/paladin/admin/v1/capability_service_pb";
import { formatDuration } from "@/lib/format/duration";

export {
  PRINCIPAL_KIND_FILTER_OPTIONS,
  PRINCIPAL_KIND_OPTIONS,
  principalKindLabel,
} from "@/lib/capabilityPrincipal";

// Shared capability vocabularies + formatting helpers. Used by the page (browse
// filter + Issue dialog) and the details renderer; lifted out of page.tsx so
// they live in one place.

/** The Issue dialog's choice that sends no TTL. */
const SERVER_DEFAULT_LABEL = "Server default";

export const TTL_OPTIONS: {
  label: string;
  value: string;
  seconds: number | null;
}[] = [
  { label: "5 minutes", value: "5m", seconds: 5 * 60 },
  { label: "1 hour", value: "1h", seconds: 60 * 60 },
  { label: "8 hours", value: "8h", seconds: 8 * 60 * 60 },
  { label: "1 day", value: "1d", seconds: 24 * 60 * 60 },
  { label: SERVER_DEFAULT_LABEL, value: "default", seconds: null },
];

/**
 * The lifetimes the Issue dialog offers, the server's default labelled with
 * what it is once known: "Server default (15 min)". It still sends no TTL,
 * so the server applies its own whatever the label says.
 */
export function ttlOptions(
  defaultTtlMs: number | undefined,
): typeof TTL_OPTIONS {
  if (defaultTtlMs === undefined) return TTL_OPTIONS;
  return TTL_OPTIONS.map((o) =>
    o.seconds === null
      ? {
          ...o,
          label: `${SERVER_DEFAULT_LABEL} (${formatDuration(defaultTtlMs)})`,
        }
      : o,
  );
}

// Capability ops: the ones limes defines, then the data plane's own, as
// sdk/testdata/capability_ops.json names them for both SDKs and the server
// (_constants.test.ts holds this list to it). Curated rather than free-form
// so operators can't typo.
export const OP_CHOICES = [
  "get",
  "put",
  "list",
  "delete",
  "share",
  "manage",
  "object:presign",
  "object:tag",
];

export const AUDIENCE_CHOICES = ["data", "admin", "iam", "mcp"];

// SPEND_DIGITS is how many decimals spend is shown to, so a sub-cent charge
// does not read as nothing.
export const SPEND_DIGITS = 4;

export function isExpired(c: Capability): boolean {
  if (!c.expiresAt) return false;
  const ms = Number(c.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}
