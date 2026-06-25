// Shared constants + pure helpers for the m2m-tokens page and its dialogs.
// No React here.
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";

export const TTL_OPTIONS: {
  label: string;
  value: string;
  seconds: number | null;
}[] = [
  { label: "30 days", value: "30d", seconds: 30 * 24 * 3600 },
  { label: "90 days", value: "90d", seconds: 90 * 24 * 3600 },
  { label: "1 year", value: "365d", seconds: 365 * 24 * 3600 },
  { label: "Server default", value: "default", seconds: null },
];

export const AUDIENCE_CHOICES = ["data", "admin", "iam", "mcp"];

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

export function isRevoked(t: APIToken): boolean {
  if (!t.revokedAt) return false;
  return Number(t.revokedAt.seconds) > 0;
}

export function isExpired(t: APIToken): boolean {
  if (!t.expiresAt) return false;
  const ms = Number(t.expiresAt.seconds) * 1000;
  return ms > 0 && ms < Date.now();
}
