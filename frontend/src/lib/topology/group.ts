// Folding siblings of one kind into a single node, as Argo CD folds a
// deployment's pods: a tenant with two hundred collections is one node saying
// so, not two hundred rows that push its buckets off the screen.

import {
  KIND_NOUN,
  worstStatus,
  type TopologyEdge,
  type TopologyGraph,
  type TopologyNode,
} from "./model";

const GROUP_PREFIX = "group:";

/** Whether a node stands for a group of siblings. */
export function isGroup(node: TopologyNode): boolean {
  return node.members !== undefined;
}

/**
 * The graph with every set of siblings larger than `above` folded into one
 * node. Siblings are nodes of one kind with exactly the same parents, so a
 * group keeps every edge its members had: from each shared parent, and to
 * every child any member had. A group whose id is in `expanded` is left
 * unfolded, so the reader can open one without opening them all.
 *
 * Folding runs until nothing more folds: once a backend's buckets are one
 * node, the tenants they served share that node as their parent and may fold
 * in turn. A group is never folded into another.
 */
export function groupSiblings(
  graph: TopologyGraph,
  above: number,
  expanded: ReadonlySet<string> = new Set(),
): TopologyGraph {
  let current = graph;
  for (;;) {
    const next = foldOnce(current, above, expanded);
    if (next === current) return current;
    current = next;
  }
}

function foldOnce(
  graph: TopologyGraph,
  above: number,
  expanded: ReadonlySet<string>,
): TopologyGraph {
  const parents = new Map<string, string[]>();
  for (const e of graph.edges) {
    const list = parents.get(e.to) ?? [];
    list.push(e.from);
    parents.set(e.to, list);
  }

  // Siblings by kind and parent set, in the order the graph lists them.
  const sets = new Map<string, TopologyNode[]>();
  for (const node of graph.nodes) {
    if (isGroup(node)) continue;
    const key = `${node.kind}|${[...new Set(parents.get(node.id) ?? [])].sort().join(",")}`;
    const set = sets.get(key) ?? [];
    set.push(node);
    sets.set(key, set);
  }

  const groupOf = new Map<string, TopologyNode>();
  for (const [key, members] of sets) {
    if (members.length <= above) continue;
    const id = GROUP_PREFIX + key;
    if (expanded.has(id)) continue;
    const { kind } = members[0];
    const group: TopologyNode = {
      id,
      kind,
      label: `${members.length} ${KIND_NOUN[kind].many}`,
      status: worstStatus(members.map((m) => m.status)),
      orphan: members.every((m) => m.orphan),
      members,
    };
    for (const m of members) groupOf.set(m.id, group);
  }
  if (groupOf.size === 0) return graph;

  const nodes: TopologyNode[] = [];
  const placed = new Set<string>();
  for (const node of graph.nodes) {
    const shown = groupOf.get(node.id) ?? node;
    if (placed.has(shown.id)) continue;
    placed.add(shown.id);
    nodes.push(shown);
  }

  const edges: TopologyEdge[] = [];
  const seen = new Set<string>();
  for (const e of graph.edges) {
    const from = groupOf.get(e.from)?.id ?? e.from;
    const to = groupOf.get(e.to)?.id ?? e.to;
    const key = `${from}\u0000${to}`;
    if (from === to || seen.has(key)) continue;
    seen.add(key);
    edges.push({ from, to });
  }
  return { nodes, edges, columns: graph.columns };
}
