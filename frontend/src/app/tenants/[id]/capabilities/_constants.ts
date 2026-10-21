import {
  PrincipalKind,
  type Capability,
} from "@/gen/paladin/admin/v1/capability_service_pb";

// Shared capability vocabularies + formatting helpers. Used by the page (browse
// filter + Issue dialog) and the details renderer; lifted out of page.tsx so
// they live in one place.

export const TTL_OPTIONS: {
  label: string;
  value: string;
  seconds: number | null;
}[] = [
  { label: "5 minutes", value: "5m", seconds: 5 * 60 },
  { label: "1 hour", value: "1h", seconds: 60 * 60 },
  { label: "8 hours", value: "8h", seconds: 8 * 60 * 60 },
  { label: "1 day", value: "1d", seconds: 24 * 60 * 60 },
  { label: "Server default", value: "default", seconds: null },
];

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

export const PRINCIPAL_KIND_OPTIONS = [
  { value: String(PrincipalKind.USER), label: "user" },
  { value: String(PrincipalKind.AGENT), label: "agent" },
  { value: String(PrincipalKind.SERVICE), label: "service" },
];

/** A principal kind's name, or its number when it is not one of the three. */
export function principalKindLabel(kind: PrincipalKind | undefined): string {
  return (
    PRINCIPAL_KIND_OPTIONS.find((o) => Number(o.value) === kind)?.label ??
    `kind:${kind}`
  );
}

// The list's kind filter: every kind, then each one.
export const PRINCIPAL_KIND_FILTER_OPTIONS = [
  { value: String(PrincipalKind.UNSPECIFIED), label: "any kind" },
  ...PRINCIPAL_KIND_OPTIONS,
];

// SPEND_DIGITS is how many decimals spend is shown to, so a sub-cent charge
// does not read as nothing.
export const SPEND_DIGITS = 4;

export function isExpired(c: Capability): boolean {
  if (!c.expiresAt) return false;
  const ms = Number(c.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}
