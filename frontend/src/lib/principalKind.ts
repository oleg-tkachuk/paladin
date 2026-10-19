// The credential types a principal comes from, by the wire names Cedar
// policies match in `principal.kind` (backend auth.PrincipalKindName*). A
// test reads them back out of the backend, so the two cannot drift.

export const PRINCIPAL_KINDS = [
  { value: "user", label: "User (signed in)" },
  { value: "api_key", label: "API key" },
  { value: "service_account", label: "Service account" },
  { value: "capability", label: "Capability token" },
] as const;

export type PrincipalKindName = (typeof PRINCIPAL_KINDS)[number]["value"];

/** What a simulated principal is when the case does not say. */
export const DEFAULT_PRINCIPAL_KIND: PrincipalKindName = "user";
