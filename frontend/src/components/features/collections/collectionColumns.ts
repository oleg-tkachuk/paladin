// The columns of a list of collections — the platform's and a tenant's —
// which each table's header and its export both read.
import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { PUBLIC_READ_LABEL } from "@/components/features/buckets/PublicReadBadge";
import {
  tenantLabel,
  type TenantLabel,
} from "@/components/features/buckets/BucketTable";
import type { CollectionSortColumn } from "@/lib/collectionSort";
import type { TableColumns } from "@/lib/table/columns";

import { USAGE_TABLE_COLUMNS } from "./CollectionUsage";

/** Every column a collection list has. */
export type CollectionColumn =
  CollectionSortColumn | typeof TENANT_COLUMN | "actions";

/** The column naming a collection's tenant: only a list across tenants has it. */
export const TENANT_COLUMN = "tenant";

/** A collection's name, and that it is public where its badge says so. */
function collectionText(c: Collection): string {
  return c.access === CollectionAccess.PUBLIC_READ
    ? `${c.collection} ${PUBLIC_READ_LABEL}`
    : c.collection;
}

/** The columns, in the order the tables render them; an unknown tenant shows its id. */
export function collectionColumns(
  tenants: ReadonlyMap<string, TenantLabel>,
): TableColumns<Collection, CollectionColumn> {
  return {
    name: { header: "Collection", text: collectionText },
    [TENANT_COLUMN]: {
      header: "Tenant",
      text: (c) => tenantLabel(tenants, c.tenantId),
    },
    displayName: { header: "Display name", text: (c) => c.displayName },
    bucket: { header: "Bucket", text: (c) => c.bucket },
    ...USAGE_TABLE_COLUMNS,
    actions: { header: "Actions" },
  };
}
