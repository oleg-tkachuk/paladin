// The collection list's facets — whose a collection is, which bucket it is
// routed to, and how its uploads complete. The list loads every collection
// in scope (listAllCollections), so the counts are over the whole of it.

import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import {
  CompletionMode,
  CompletionModeSchema,
} from "@/gen/paladin/common/v1/resource_pb";
import {
  tenantLabel,
  type TenantLabel,
} from "@/components/features/buckets/BucketTable";
import type { Facet, FacetOption } from "@/lib/table/facets";

export const COLLECTION_FACET_TENANT = "tenant";
export const COLLECTION_FACET_BUCKET = "bucket";
export const COLLECTION_FACET_COMPLETION = "completion";

/** A bucket's resource name, storageBackends/{backend}/buckets/{bucket}. */
const BUCKET_NAME = /^storageBackends\/([^/]+)\/buckets\/([^/]+)$/;

/** A bucket in words: "primary / caryon-public"; none, "No bucket". */
export function bucketLabel(name: string): string {
  if (!name) return "No bucket";
  const m = BUCKET_NAME.exec(name);
  return m ? `${m[1]} / ${m[2]}` : name;
}

/** A completion mode's value as the facet names it: "EXPLICIT". */
function completionValue(mode: CompletionMode): string {
  return CompletionModeSchema.value[mode]?.localName ?? "";
}

/** The completion modes the facet offers. */
const COMPLETION_OPTIONS: readonly FacetOption[] = [
  { value: completionValue(CompletionMode.IMPLICIT), label: "Implicit" },
  { value: completionValue(CompletionMode.EXPLICIT), label: "Explicit" },
];

/**
 * The collection list's facets, its tenants named as `tenants` names them.
 * Built again only when the names change, which the page memoises.
 */
export function collectionFacets(
  tenants: ReadonlyMap<string, TenantLabel>,
): readonly Facet<Collection>[] {
  return [
    {
      id: COLLECTION_FACET_TENANT,
      label: "Tenant",
      values: (c) => [c.tenantId],
      optionLabel: (id) => tenantLabel(tenants, id),
    },
    {
      id: COLLECTION_FACET_BUCKET,
      label: "Bucket",
      values: (c) => [c.bucket],
      optionLabel: bucketLabel,
    },
    {
      id: COLLECTION_FACET_COMPLETION,
      label: "Completion",
      values: (c) => [completionValue(c.completionMode)],
      options: COMPLETION_OPTIONS,
    },
  ];
}

/** One tenant's collections' facets: all but the tenant, which is fixed. */
export const TENANT_COLLECTION_FACETS: readonly Facet<Collection>[] =
  collectionFacets(new Map()).filter((f) => f.id !== COLLECTION_FACET_TENANT);
