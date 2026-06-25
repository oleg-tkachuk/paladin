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

// Capability ops vocabulary mirrored from internal/capability/caveats.go.
// Keep the list curated rather than free-form so operators can't typo.
export const OP_CHOICES = [
  "get",
  "put",
  "list",
  "delete",
  "presign",
  "tag",
  "search",
  "embed",
  "share",
  "manage",
];

export const AUDIENCE_CHOICES = ["data", "admin", "iam", "mcp"];

export const PRINCIPAL_KIND_OPTIONS = [
  { value: String(PrincipalKind.USER), label: "user" },
  { value: String(PrincipalKind.AGENT), label: "agent" },
  { value: String(PrincipalKind.SERVICE), label: "service" },
];

export function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  if (!ts) return "—";
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return "—";
  try {
    return new Date(ms).toISOString().replace("T", " ").replace(".000Z", "Z");
  } catch {
    return "—";
  }
}

export function isExpired(c: Capability): boolean {
  if (!c.expiresAt) return false;
  const ms = Number(c.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}
