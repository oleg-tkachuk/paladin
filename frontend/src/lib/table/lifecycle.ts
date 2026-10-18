// The lifecycle every credential-like row shares — an OAuth grant, an M2M
// token: live, ended on purpose, or lapsed. One vocabulary, so each table
// names, colours and filters the states alike.
import type { ToolbarFilterOption } from "@/components/ui/TableToolbar";

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

/** Which rows a lifecycle filter keeps: one state, or every one. */
export type LifecycleFilter = Lifecycle | "all";

export const LIFECYCLE_FILTERS: readonly ToolbarFilterOption<LifecycleFilter>[] =
  [
    { value: "active", label: "Active" },
    { value: "revoked", label: "Revoked" },
    { value: "expired", label: "Expired" },
    { value: "all", label: "All" },
  ];

/** What a table shows first: the rows still in use. */
export const DEFAULT_LIFECYCLE_FILTER: LifecycleFilter = "active";

/** The label the toolbar gives the filter. */
export const LIFECYCLE_FILTER_LABEL = "State";

export const keepsLifecycle = (filter: LifecycleFilter, state: Lifecycle) =>
  filter === "all" || filter === state;
