// The lifecycle every credential-like row shares — an OAuth grant, an M2M
// token: live, ended on purpose, or lapsed. One vocabulary, so each table
// names, colours and filters the states alike.
import type { Facet } from "@/lib/table/facets";
import { StatusTone } from "@/lib/ui/status";

export type Lifecycle = "active" | "revoked" | "expired";

/** A lifecycle state's badge: its name and colour. */
export const LIFECYCLE_BADGE = {
  active: { label: "active", variant: "success" },
  revoked: { label: "revoked", variant: "destructive" },
  expired: { label: "expired", variant: "outline" },
} as const satisfies Record<
  Lifecycle,
  { label: string; variant: "success" | "destructive" | "outline" }
>;

/**
 * The state facet of a table of such rows, opening on the rows still in use.
 * Expired takes the neutral grey its outline badge wears: lapsed, not broken.
 */
export function lifecycleFacet<R>(state: (row: R) => Lifecycle): Facet<R> {
  return {
    id: "state",
    label: "State",
    values: (row) => [state(row)],
    options: [
      { value: "active", label: "Active", tone: StatusTone.Success },
      { value: "revoked", label: "Revoked", tone: StatusTone.Danger },
      { value: "expired", label: "Expired", tone: StatusTone.Unknown },
    ],
    initial: ["active"],
  };
}
