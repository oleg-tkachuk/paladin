// The kinds of principal a capability is issued to, as every capability list,
// detail and diagram names them.
import { PrincipalKind } from "@/gen/paladin/admin/v1/capability_service_pb";

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
