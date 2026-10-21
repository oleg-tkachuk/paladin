import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { timestampToDate } from "@/lib/utils";

/** The usage columns' sort keys. */
export const USAGE_SORT_COLUMNS = ["objects", "size", "lastUpload"] as const;
export type UsageSortColumn = (typeof USAGE_SORT_COLUMNS)[number];

/** Every column a collection list sorts by, its own and its usage. */
export const COLLECTION_SORT_COLUMNS = [
  "name",
  "displayName",
  "bucket",
  ...USAGE_SORT_COLUMNS,
] as const;
export type CollectionSortColumn = (typeof COLLECTION_SORT_COLUMNS)[number];

/**
 * The value a collection sorts by: its name, display name or bucket as text;
 * its available objects, their bytes, or when the newest was stored as a
 * number, 0 with none, so an empty collection sorts first ascending.
 */
export function collectionSortValue(
  c: Collection,
  column: CollectionSortColumn,
): string | number {
  const u = c.usage;
  switch (column) {
    case "name":
      return c.collection;
    case "displayName":
      return c.displayName || "";
    case "bucket":
      return c.bucket || "";
    case "objects":
      return Number(u?.objectCount ?? 0);
    case "size":
      return Number(u?.totalBytes ?? 0);
    case "lastUpload":
      return u?.lastCommittedAt
        ? timestampToDate(u.lastCommittedAt).getTime()
        : 0;
  }
}

/** list sorted by column, ascending or not; a copy, list as it was. */
export function sortCollections(
  list: readonly Collection[],
  column: CollectionSortColumn | null,
  direction: "asc" | "desc" | null,
): Collection[] {
  const out = [...list];
  if (!column || !direction) return out;
  const dir = direction === "asc" ? 1 : -1;
  return out.sort((a, b) => {
    const va = collectionSortValue(a, column);
    const vb = collectionSortValue(b, column);
    return va < vb ? -dir : va > vb ? dir : 0;
  });
}
