// The audit log's facets — the plane a call came in on, how it ended and the
// service it called — as the server narrows and counts them
// (ListAuditLogRequest.facets, ListAuditLogResponse.facet_counts). The log is
// paged on the server, so the counts are its, not the loaded entries'.

import type { FacetCount } from "@/gen/paladin/common/v1/facet_pb";
import {
  AuditOutcome,
  AuditOutcomeSchema,
} from "@/gen/paladin/admin/v1/audit_service_pb";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import {
  groupFacetCounts,
  type Facet,
  type FacetCounts,
  type FacetOption,
  type FacetSelection,
} from "@/lib/table/facets";
import { StatusTone } from "@/lib/ui/status";

// The facets the server narrows and counts the log by, as it names them
// (admindomain.AuditFacetPlane, AuditFacetOutcome, AuditFacetService).
export const AUDIT_FACET_PLANE = "plane";
export const AUDIT_FACET_OUTCOME = "outcome";
export const AUDIT_FACET_SERVICE = "service";

/** An outcome's value as the facet names it: the enum's own name, "ERROR". */
function outcomeValue(outcome: AuditOutcome): string {
  return AuditOutcomeSchema.value[outcome]?.localName ?? "";
}

/** The outcomes the facet offers. */
const AUDIT_OUTCOME_OPTIONS: readonly FacetOption[] = [
  {
    value: outcomeValue(AuditOutcome.OK),
    label: "Succeeded",
    tone: StatusTone.Success,
  },
  {
    value: outcomeValue(AuditOutcome.ERROR),
    label: "Failed",
    tone: StatusTone.Danger,
  },
];

/** Prefixes the server puts on a plane's audience and a service's package. */
const AUDIENCE_PREFIX = "paladin-";
const SERVICE_PACKAGE = /^paladin\.([a-z]+)\.v\d+\.(.+)$/;

/** A plane in words: "paladin-iam" reads "iam". */
export function planeLabel(audience: string): string {
  return audience.startsWith(AUDIENCE_PREFIX)
    ? audience.slice(AUDIENCE_PREFIX.length)
    : audience || "none";
}

/**
 * A service in words: "paladin.admin.v1.BucketService" reads "BucketService
 * (admin)"; one of the server's own actions, "iam.bootstrap_admin", as it is.
 */
export function serviceLabel(service: string): string {
  const m = SERVICE_PACKAGE.exec(service);
  return m ? `${m[2]} (${m[1]})` : service;
}

/** An entry's service, as the server's audit_service names it. */
export function auditService(action: string): string {
  const parts = action.split("/");
  return action.startsWith("/") && parts.length === 3 ? parts[1] : action;
}

/** The audit log's facets. */
export const AUDIT_FACETS: readonly Facet<AuditLogEntry>[] = [
  {
    id: AUDIT_FACET_PLANE,
    label: "Plane",
    values: (e) => [e.actorAudience],
    optionLabel: planeLabel,
  },
  {
    id: AUDIT_FACET_OUTCOME,
    label: "Outcome",
    values: (e) => [
      outcomeValue(e.errorMessage ? AuditOutcome.ERROR : AuditOutcome.OK),
    ],
    options: AUDIT_OUTCOME_OPTIONS,
  },
  {
    id: AUDIT_FACET_SERVICE,
    label: "Service",
    values: (e) => [auditService(e.action)],
    optionLabel: serviceLabel,
  },
];

/**
 * The server's counts as the facets name their answers: an outcome comes by
 * its wire name, "AUDIT_OUTCOME_ERROR", and is offered as "ERROR".
 */
export function auditFacetCounts(counts: readonly FacetCount[]): FacetCounts {
  return groupFacetCounts(counts, (c) => {
    if (c.facet !== AUDIT_FACET_OUTCOME) return c.value;
    return (
      AuditOutcomeSchema.values.find((v) => v.name === c.value)?.localName ??
      c.value
    );
  });
}

/** The facet choices as the request names them. */
export function auditFacetsRequest(selection: FacetSelection): {
  planes: string[];
  outcomes: AuditOutcome[];
  services: string[];
} {
  const outcomes = (selection[AUDIT_FACET_OUTCOME] ?? []).flatMap((value) => {
    const v = AuditOutcomeSchema.values.find((e) => e.localName === value);
    return v ? [v.number as AuditOutcome] : [];
  });
  return {
    planes: [...(selection[AUDIT_FACET_PLANE] ?? [])],
    outcomes,
    services: [...(selection[AUDIT_FACET_SERVICE] ?? [])],
  };
}
