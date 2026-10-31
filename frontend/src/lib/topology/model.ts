// The topology graph: what a tenant or the platform is made of, as nodes and
// the edges between them, after Argo CD's application resource tree. Pure, so
// the builders, the grouping and the layout can be tested without a page, and
// the drawing only places what they decided.

import { STATUS_ORDER, type StatusTone } from "@/lib/ui/status";

/** What a node stands for. */
export type NodeKind =
  | "tenant"
  | "collection"
  | "bucket"
  | "backend"
  // A resource's own Cedar layer, and a principal holding live capabilities:
  // a tenant's access, drawn as a branch off it.
  | "policy"
  | "principal";

/** A node's state: the tone it is drawn in and the words that name it. */
export interface NodeStatus {
  tone: StatusTone;
  label: string;
}

/** A further page about a node, offered in its menu. */
export interface NodeLink {
  label: string;
  href: string;
}

export interface TopologyNode {
  /** Unique in its graph: the kind and the entity's id, e.g. "bucket:be/b". */
  id: string;
  kind: NodeKind;
  label: string;
  /** A second line saying what the node is to its neighbours. */
  detail?: string;
  /** Absent for a thing that has no state of its own, such as a collection. */
  status?: NodeStatus;
  /** The node's one page (ADR-0033); a group has none. */
  href?: string;
  links?: readonly NodeLink[];
  /** Nothing uses it: a bucket no collection is bound to, a tenant with no bucket. */
  orphan?: boolean;
  /** For a group, the siblings it stands for. */
  members?: readonly TopologyNode[];
}

/** An edge reads from what holds or serves to what it holds or serves. */
export interface TopologyEdge {
  from: string;
  to: string;
}

export interface TopologyGraph {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  /**
   * The column each kind is drawn in, left to right. A node is never drawn
   * left of its kind's column, so an orphan, which has no edge to rank it
   * by, still stands with its kind rather than among the roots.
   */
  columns: readonly NodeKind[];
}

/** How each kind is named, one and several. */
export const KIND_NOUN: Record<NodeKind, { one: string; many: string }> = {
  tenant: { one: "Tenant", many: "tenants" },
  collection: { one: "Collection", many: "collections" },
  bucket: { one: "Bucket", many: "buckets" },
  backend: { one: "Storage backend", many: "storage backends" },
  policy: { one: "Policy", many: "policies" },
  principal: { one: "Principal", many: "principals" },
};

/**
 * The status that most needs a look, in STATUS_ORDER: a failed probe
 * outranks a backend that is merely untested. Undefined when none has one.
 */
export function worstStatus(
  statuses: readonly (NodeStatus | undefined)[],
): NodeStatus | undefined {
  let worst: NodeStatus | undefined;
  for (const s of statuses) {
    if (!s) continue;
    if (
      !worst ||
      STATUS_ORDER.indexOf(s.tone) < STATUS_ORDER.indexOf(worst.tone)
    )
      worst = s;
  }
  return worst;
}

/**
 * Whether a node answers a filter: its name, its second line, its kind or
 * its state holds the text, ignoring case. A group answers when any of its
 * members does, so filtering never hides a match inside a collapsed group.
 */
export function nodeMatches(node: TopologyNode, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  if (node.members) return node.members.some((m) => nodeMatches(m, q));
  return [
    node.label,
    node.detail ?? "",
    KIND_NOUN[node.kind].one,
    node.status?.label ?? "",
  ].some((s) => s.toLowerCase().includes(q));
}

/** An edge's key: the same in the drawing and in a neighbourhood. */
export function edgeKey(edge: TopologyEdge): string {
  return `${edge.from}>${edge.to}`;
}

/** A node with its parents and children, and the edges that join them. */
export interface Neighbourhood {
  nodes: ReadonlySet<string>;
  edges: ReadonlySet<string>;
}

/**
 * The node `id` with what holds or serves it and what it holds or serves:
 * the nodes one edge away either side, and those edges. What hovering a node
 * lights up, so its place in the graph reads among the crossing curves.
 */
export function neighbourhood(
  graph: Pick<TopologyGraph, "edges">,
  id: string,
): Neighbourhood {
  const nodes = new Set([id]);
  const edges = new Set<string>();
  for (const e of graph.edges) {
    if (e.from !== id && e.to !== id) continue;
    nodes.add(e.from === id ? e.to : e.from);
    edges.add(edgeKey(e));
  }
  return { nodes, edges };
}
