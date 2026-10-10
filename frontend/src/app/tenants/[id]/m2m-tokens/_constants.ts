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

// The planes a token minted here may name. Not "admin": that plane admits an
// API token only when it carries roles, which this dialog cannot grant, and
// the server refuses a roleless token naming it.
export const AUDIENCE_CHOICES = ["data", "iam", "mcp"];

// Resource-scope wire forms accepted by APITokenService.Create. A token
// with NO scopes has full tenant access (unchanged); adding scopes
// confines it to matching resources. Each scope is either the wildcard
// "*" or "<prefix>:<value>" for one of these prefixes:
//
//   tenant:<tenant_uuid>
//   backend:<backend_id>
//   bucket:<bucket_name>
//   object_key:<bucket_name>/<object_key>
//
// The list mirrors the backend's mint-time validation — invalid scopes
// are rejected there too, so this is a client-side pre-flight only.
export const SCOPE_PREFIXES = [
  "tenant",
  "backend",
  "bucket",
  "object_key",
] as const;

// parseScopes splits a free-form comma-, space-, or newline-separated
// string into trimmed, non-empty scope tokens (the wire form sent to the
// backend).
export function parseScopes(raw: string): string[] {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

// isValidScope returns true when `scope` is exactly "*" or has one of the
// SCOPE_PREFIXES followed by ":" and a non-empty value. Mirrors the
// backend's mint-time rejection so the dialog can block submit early.
export function isValidScope(scope: string): boolean {
  const s = scope.trim();
  if (s === "*") return true;
  const idx = s.indexOf(":");
  if (idx <= 0) return false;
  const prefix = s.slice(0, idx);
  const value = s.slice(idx + 1);
  return (
    (SCOPE_PREFIXES as readonly string[]).includes(prefix) && value.length > 0
  );
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
