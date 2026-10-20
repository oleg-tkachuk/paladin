// A layered layout, left to right, as dagre draws Argo CD's resource tree
// with rankdir LR. Written here rather than taken as a dependency: the graphs
// are a few hundred nodes at most, and three steps — rank by longest path,
// order each rank by the barycentre of its neighbours, place on a grid — are
// all they need. Deterministic: the same graph always lands the same way, so
// a refetch does not reshuffle what the reader was looking at.

import type { TopologyEdge, TopologyGraph, TopologyNode } from "./model";

/** A node's box, in the graph's own units (pixels at 100 %). */
export const NODE_WIDTH = 280;
export const NODE_HEIGHT = 52;
// The gap between columns leaves room for an edge's curve to read as one.
const RANK_GAP = 96;
const ROW_GAP = 16;
const PADDING = 16;
// Down-and-up sweeps of the barycentre pass. Each sweep can only remove
// crossings the previous one left; past four the graphs here do not change.
const SWEEPS = 4;

export interface PlacedNode {
  node: TopologyNode;
  /** Top-left corner. */
  x: number;
  y: number;
  /** The column, from 0. */
  rank: number;
}

export interface PlacedEdge {
  from: string;
  to: string;
  /** An SVG path from the source's right side to the target's left. */
  path: string;
}

export interface TopologyLayout {
  /** In reading order: column by column, top to bottom. Tab order follows it. */
  nodes: PlacedNode[];
  edges: PlacedEdge[];
  width: number;
  height: number;
}

/**
 * Each node's column: one past its furthest parent, and never left of its
 * kind's column. Kahn's order guarantees every parent is ranked first; a
 * cycle, which the builders never make, is broken at the first node left
 * in the graph's own order rather than looping.
 */
function rankNodes(graph: TopologyGraph): Map<string, number> {
  const ids = new Set(graph.nodes.map((n) => n.id));
  const edges = graph.edges.filter((e) => ids.has(e.from) && ids.has(e.to));
  const indegree = new Map(graph.nodes.map((n) => [n.id, 0]));
  const children = new Map<string, string[]>();
  for (const e of edges) {
    indegree.set(e.to, (indegree.get(e.to) ?? 0) + 1);
    children.set(e.from, [...(children.get(e.from) ?? []), e.to]);
  }
  const floor = (n: TopologyNode) => Math.max(graph.columns.indexOf(n.kind), 0);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));

  const rank = new Map(graph.nodes.map((n) => [n.id, floor(n)]));
  const done = new Set<string>();
  const ready = graph.nodes.filter((n) => indegree.get(n.id) === 0);
  const remaining = [...graph.nodes];
  while (done.size < graph.nodes.length) {
    const node = ready.shift() ?? remaining.find((n) => !done.has(n.id))!;
    if (done.has(node.id)) continue;
    done.add(node.id);
    for (const child of children.get(node.id) ?? []) {
      if (done.has(child)) continue;
      rank.set(child, Math.max(rank.get(child)!, rank.get(node.id)! + 1));
      const left = indegree.get(child)! - 1;
      indegree.set(child, left);
      if (left === 0) ready.push(byId.get(child)!);
    }
  }
  return rank;
}

/** The mean position of a node's neighbours, or undefined when it has none. */
function barycentre(
  neighbours: readonly string[] | undefined,
  position: ReadonlyMap<string, number>,
): number | undefined {
  const at = (neighbours ?? [])
    .map((n) => position.get(n))
    .filter((p): p is number => p !== undefined);
  return at.length ? at.reduce((a, b) => a + b, 0) / at.length : undefined;
}

/**
 * Reorders each column by the barycentre of its neighbours in the columns
 * before it (a down sweep) or after it (an up sweep). A node with no
 * neighbour that way keeps its place; ties keep the current order, so the
 * result depends on the graph alone.
 */
function orderRanks(
  layers: string[][],
  edges: readonly TopologyEdge[],
  rank: ReadonlyMap<string, number>,
): void {
  const parents = new Map<string, string[]>();
  const children = new Map<string, string[]>();
  for (const e of edges) {
    parents.set(e.to, [...(parents.get(e.to) ?? []), e.from]);
    children.set(e.from, [...(children.get(e.from) ?? []), e.to]);
  }
  const position = new Map<string, number>();
  const index = () =>
    layers.forEach((layer) => layer.forEach((id, i) => position.set(id, i)));
  index();

  const reorder = (r: number, by: Map<string, string[]>, before: boolean) => {
    const layer = layers[r];
    const keyed = layer.map((id, i) => {
      const side = (by.get(id) ?? []).filter((n) =>
        before ? rank.get(n)! < r : rank.get(n)! > r,
      );
      return { id, i, key: barycentre(side, position) ?? i };
    });
    keyed.sort((a, b) => a.key - b.key || a.i - b.i);
    layers[r] = keyed.map((k) => k.id);
    layers[r].forEach((id, i) => position.set(id, i));
  };

  for (let sweep = 0; sweep < SWEEPS; sweep++) {
    if (sweep % 2 === 0) {
      for (let r = 1; r < layers.length; r++) reorder(r, parents, true);
    } else {
      for (let r = layers.length - 2; r >= 0; r--) reorder(r, children, false);
    }
  }
}

/**
 * A smooth curve from one node's right side to another's left: a cubic whose
 * control points sit halfway across, so it leaves and arrives level.
 */
function edgePath(from: PlacedNode, to: PlacedNode): string {
  const x1 = from.x + NODE_WIDTH;
  const y1 = from.y + NODE_HEIGHT / 2;
  const x2 = to.x;
  const y2 = to.y + NODE_HEIGHT / 2;
  const mid = (x1 + x2) / 2;
  return `M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`;
}

/** Where every node and edge of `graph` is drawn. */
export function layoutGraph(graph: TopologyGraph): TopologyLayout {
  if (graph.nodes.length === 0) {
    return { nodes: [], edges: [], width: 0, height: 0 };
  }
  const rank = rankNodes(graph);
  const ranks = Math.max(...rank.values()) + 1;
  const layers: string[][] = Array.from({ length: ranks }, () => []);
  for (const n of graph.nodes) layers[rank.get(n.id)!].push(n.id);
  orderRanks(layers, graph.edges, rank);

  // Columns are centred on the tallest, so a lone root sits level with the
  // middle of what it holds rather than at the top.
  const rows = Math.max(...layers.map((l) => l.length));
  const step = NODE_HEIGHT + ROW_GAP;
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const nodes: PlacedNode[] = layers.flatMap((layer, r) => {
    const offset = ((rows - layer.length) * step) / 2;
    return layer.map((id, i) => ({
      node: byId.get(id)!,
      x: PADDING + r * (NODE_WIDTH + RANK_GAP),
      y: PADDING + offset + i * step,
      rank: r,
    }));
  });

  const placed = new Map(nodes.map((p) => [p.node.id, p]));
  const edges: PlacedEdge[] = graph.edges.flatMap((e) => {
    const from = placed.get(e.from);
    const to = placed.get(e.to);
    return from && to ? [{ ...e, path: edgePath(from, to) }] : [];
  });

  return {
    nodes,
    edges,
    width: PADDING * 2 + ranks * NODE_WIDTH + (ranks - 1) * RANK_GAP,
    height: PADDING * 2 + rows * NODE_HEIGHT + (rows - 1) * ROW_GAP,
  };
}
