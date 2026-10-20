import { describe, expect, it } from "vitest";

import {
  NODE_HEIGHT,
  NODE_WIDTH,
  layoutGraph,
  type TopologyLayout,
} from "./layout";
import type { NodeKind, TopologyEdge, TopologyGraph } from "./model";

function graph(
  nodes: [NodeKind, string][],
  edges: [string, string][],
  columns: NodeKind[] = [],
): TopologyGraph {
  return {
    nodes: nodes.map(([kind, id]) => ({ id, kind, label: id })),
    edges: edges.map(([from, to]) => ({ from, to })),
    columns,
  };
}

const rankOf = (l: TopologyLayout) =>
  Object.fromEntries(l.nodes.map((p) => [p.node.id, p.rank]));
const yOf = (l: TopologyLayout) =>
  new Map(l.nodes.map((p) => [p.node.id, p.y]));

/** Pairs of edges between the same two columns whose ends swap order. */
function crossings(l: TopologyLayout, edges: readonly TopologyEdge[]): number {
  const y = yOf(l);
  const rank = rankOf(l);
  let n = 0;
  for (let i = 0; i < edges.length; i++) {
    for (let j = i + 1; j < edges.length; j++) {
      const a = edges[i];
      const b = edges[j];
      if (rank[a.from] !== rank[b.from] || rank[a.to] !== rank[b.to]) continue;
      const dy1 = y.get(a.from)! - y.get(b.from)!;
      const dy2 = y.get(a.to)! - y.get(b.to)!;
      if (dy1 * dy2 < 0) n++;
    }
  }
  return n;
}

describe("layoutGraph", () => {
  it("lays nothing out for an empty graph", () => {
    expect(layoutGraph(graph([], []))).toEqual({
      nodes: [],
      edges: [],
      width: 0,
      height: 0,
    });
  });

  // Longest path: d is two steps from a by one route and one by another, and
  // sits past the longer, so no edge runs right to left.
  it("ranks each node one past its furthest parent", () => {
    const l = layoutGraph(
      graph(
        [
          ["tenant", "a"],
          ["collection", "b"],
          ["bucket", "c"],
          ["bucket", "d"],
        ],
        [
          ["a", "b"],
          ["b", "d"],
          ["a", "d"],
          ["a", "c"],
        ],
      ),
    );
    expect(rankOf(l)).toEqual({ a: 0, b: 1, c: 1, d: 2 });
  });

  // An unused bucket has no parent; it stands with the buckets, not the root.
  it("never draws a node left of its kind's column", () => {
    const l = layoutGraph(
      graph(
        [
          ["tenant", "t"],
          ["bucket", "orphan"],
        ],
        [],
        ["tenant", "collection", "bucket"],
      ),
    );
    expect(rankOf(l)).toEqual({ t: 0, orphan: 2 });
  });

  it("places columns left to right on a grid of node boxes", () => {
    const l = layoutGraph(
      graph(
        [
          ["tenant", "a"],
          ["bucket", "b"],
        ],
        [["a", "b"]],
      ),
    );
    const [a, b] = l.nodes;
    expect(b.x - a.x).toBeGreaterThan(NODE_WIDTH);
    expect(a.y).toBe(b.y);
    expect(l.width).toBeGreaterThanOrEqual(b.x + NODE_WIDTH);
    expect(l.height).toBeGreaterThanOrEqual(b.y + NODE_HEIGHT);
  });

  it("centres a short column on the tallest", () => {
    const l = layoutGraph(
      graph(
        [
          ["tenant", "t"],
          ["bucket", "1"],
          ["bucket", "2"],
          ["bucket", "3"],
        ],
        [
          ["t", "1"],
          ["t", "2"],
          ["t", "3"],
        ],
      ),
    );
    const y = yOf(l);
    expect(y.get("t")).toBe(y.get("2"));
  });

  // Two parents listed a, b whose children are listed in the opposite
  // order: drawn as listed, both edges cross; the barycentre pass uncrosses
  // them.
  it("orders a column to remove crossings", () => {
    const g = graph(
      [
        ["tenant", "a"],
        ["tenant", "b"],
        ["bucket", "y"],
        ["bucket", "x"],
        ["backend", "p"],
        ["backend", "q"],
      ],
      [
        ["a", "x"],
        ["b", "y"],
        ["y", "p"],
        ["x", "q"],
      ],
    );
    const l = layoutGraph(g);
    expect(crossings(l, g.edges)).toBe(0);
    const y = yOf(l);
    expect(y.get("x")!).toBeLessThan(y.get("y")!);
  });

  it("lists nodes column by column, top to bottom, for tab order", () => {
    const l = layoutGraph(
      graph(
        [
          ["bucket", "late"],
          ["tenant", "root"],
          ["bucket", "early"],
        ],
        [
          ["root", "early"],
          ["root", "late"],
        ],
      ),
    );
    const reading = [...l.nodes].sort((p, q) => p.x - q.x || p.y - q.y);
    expect(l.nodes).toEqual(reading);
    expect(l.nodes[0].node.id).toBe("root");
  });

  it("is deterministic", () => {
    const g = graph(
      [
        ["tenant", "a"],
        ["bucket", "b"],
        ["bucket", "c"],
      ],
      [
        ["a", "c"],
        ["a", "b"],
      ],
    );
    expect(layoutGraph(g)).toEqual(layoutGraph(g));
  });

  // The builders never make a cycle; if one appears, it is broken rather
  // than looped on.
  it("terminates on a cycle", () => {
    const l = layoutGraph(
      graph(
        [
          ["bucket", "a"],
          ["bucket", "b"],
        ],
        [
          ["a", "b"],
          ["b", "a"],
        ],
      ),
    );
    expect(l.nodes).toHaveLength(2);
  });

  it("drops an edge to a node the graph does not hold", () => {
    const l = layoutGraph(graph([["tenant", "a"]], [["a", "ghost"]]));
    expect(l.edges).toEqual([]);
  });

  // A smooth curve that leaves the source's right side and arrives level at
  // the target's left.
  it("draws an edge as a cubic from right side to left side", () => {
    const l = layoutGraph(
      graph(
        [
          ["tenant", "a"],
          ["bucket", "b"],
          ["bucket", "c"],
        ],
        [
          ["a", "b"],
          ["a", "c"],
        ],
      ),
    );
    const [a, b] = l.nodes;
    const x1 = a.x + NODE_WIDTH;
    const y1 = a.y + NODE_HEIGHT / 2;
    const x2 = b.x;
    const y2 = b.y + NODE_HEIGHT / 2;
    const mid = (x1 + x2) / 2;
    expect(l.edges[0].path).toBe(
      `M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`,
    );
  });
});
