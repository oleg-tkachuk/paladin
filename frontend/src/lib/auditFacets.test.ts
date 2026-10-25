import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { FacetCountSchema } from "@/gen/paladin/common/v1/facet_pb";
import {
  AuditOutcome,
  AuditOutcomeSchema,
} from "@/gen/paladin/admin/v1/audit_service_pb";
import { countedOptions } from "@/lib/table/facets";

import {
  AUDIT_FACETS,
  AUDIT_FACET_OUTCOME,
  AUDIT_FACET_PLANE,
  AUDIT_FACET_SERVICE,
  auditFacetCounts,
  auditFacetsRequest,
  auditService,
  planeLabel,
  serviceLabel,
} from "./auditFacets";

const [, OUTCOME, SERVICE] = AUDIT_FACETS;
const BUCKET_ACTION = "/paladin.admin.v1.BucketService/CreateBucket";
const BUCKET_SERVICE = "paladin.admin.v1.BucketService";
const BOOTSTRAP = "iam.bootstrap_admin";

// An outcome crosses the wire by its AuditOutcome name in a count and by its
// number in a request; a slip between them is a menu of zeros.
describe("audit facets", () => {
  it.each([AuditOutcome.OK, AuditOutcome.ERROR])(
    "outcome %s goes from a count to a choice to a request",
    (outcome) => {
      const wire = AuditOutcomeSchema.value[outcome].name;
      const counts = auditFacetCounts([
        create(FacetCountSchema, {
          facet: AUDIT_FACET_OUTCOME,
          value: wire,
          count: 5n,
        }),
      ]);
      const option = countedOptions(
        OUTCOME,
        counts.get(AUDIT_FACET_OUTCOME) ?? new Map(),
        [],
      ).find((o) => o.count === 5);
      expect(option).toBeDefined();
      expect(
        auditFacetsRequest({ [AUDIT_FACET_OUTCOME]: [option!.value] }).outcomes,
      ).toEqual([outcome]);
    },
  );

  it("names a service as the server's audit_service does", () => {
    expect(auditService(BUCKET_ACTION)).toBe(BUCKET_SERVICE);
    expect(auditService(BOOTSTRAP)).toBe(BOOTSTRAP);
  });

  it("puts planes and services in words", () => {
    expect(planeLabel("paladin-iam")).toBe("iam");
    expect(planeLabel("")).toBe("none");
    expect(serviceLabel(BUCKET_SERVICE)).toBe("BucketService (admin)");
    expect(serviceLabel(BOOTSTRAP)).toBe(BOOTSTRAP);
    const counts = auditFacetCounts([
      create(FacetCountSchema, {
        facet: AUDIT_FACET_SERVICE,
        value: BUCKET_SERVICE,
        count: 2n,
      }),
    ]);
    expect(
      countedOptions(SERVICE, counts.get(AUDIT_FACET_SERVICE) ?? new Map(), []),
    ).toEqual([
      { value: BUCKET_SERVICE, label: "BucketService (admin)", count: 2 },
    ]);
  });

  it("asks for planes and services by name, and nothing a choice does not name", () => {
    expect(
      auditFacetsRequest({
        [AUDIT_FACET_PLANE]: ["paladin-admin"],
        [AUDIT_FACET_SERVICE]: [BUCKET_SERVICE],
        [AUDIT_FACET_OUTCOME]: ["NOT_AN_OUTCOME"],
      }),
    ).toEqual({
      planes: ["paladin-admin"],
      outcomes: [],
      services: [BUCKET_SERVICE],
    });
  });
});
