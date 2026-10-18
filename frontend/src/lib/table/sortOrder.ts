// A sort direction, as a list RPC's sort_order carries it. No direction is
// UNSPECIFIED, which every server-sorted list reads as its own default.
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import type { SortDirection } from "@/hooks/useTableSort";

export function toSortOrder(d: SortDirection | undefined): SortOrder {
  if (d === "asc") return SortOrder.ASC;
  if (d === "desc") return SortOrder.DESC;
  return SortOrder.UNSPECIFIED;
}
