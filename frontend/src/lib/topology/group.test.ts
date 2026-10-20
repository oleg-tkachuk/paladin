import { describe, expect, it } from "vitest";

import { StatusTone } from "@/lib/ui/status";

import { groupSiblings, isGroup } from "./group";
import type { NodeKind, TopologyGraph, TopologyNode } from "./model";

const node = (
  kind: NodeKind,
  id: string,
  extra: Partial<TopologyNode> = {},
): TopologyNode => ({ id: `${kind}:${id}`, kind, label: id, ...extra });

/** A tenant with `n` collections, all in one bucket. */
function fanOut(n: number): TopologyGraph {
  const collections = Array.from({ length: n }, (_, i) =>
    node("collection", `c${i}`),
  );
  return {
    nodes: [node("tenant", "t"), ...collections, node("bucket", "b")],
    edges: [
      ...collections.map((c) => ({ from: "tenant:t", to: c.id })),
      ...collections.map((c) => ({ from: c.id, to: "bucket:b" })),
    ],
    columns: ["tenant", "collection", "bucket"],
  };
}

describe("groupSiblings", () => {
  it("leaves siblings at or under the threshold alone", () => {
    const g = fanOut(3);
    expect(groupSiblings(g, 3)).toBe(g);
  });

  it("folds siblings over the threshold into one node that names them", () => {
    const g = groupSiblings(fanOut(4), 3);
    const groups = g.nodes.filter(isGroup);
    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({
      kind: "collection",
      label: "4 collections",
    });
    expect(groups[0].members).toHaveLength(4);
    expect(g.nodes.map((n) => n.id)).toEqual([
      "tenant:t",
      groups[0].id,
      "bucket:b",
    ]);
  });

  // The group keeps every edge its members had, once each.
  it("keeps the members' edges, deduplicated", () => {
    const g = groupSiblings(fanOut(5), 3);
    const group = g.nodes.find(isGroup)!;
    expect(g.edges).toEqual([
      { from: "tenant:t", to: group.id },
      { from: group.id, to: "bucket:b" },
    ]);
  });

  // The parents are of two kinds, so they do not fold either.
  it("does not fold nodes whose parents differ", () => {
    const g: TopologyGraph = {
      nodes: [
        node("tenant", "a"),
        node("backend", "b"),
        node("collection", "1"),
        node("collection", "2"),
      ],
      edges: [
        { from: "tenant:a", to: "collection:1" },
        { from: "backend:b", to: "collection:2" },
      ],
      columns: ["tenant", "backend", "collection"],
    };
    expect(groupSiblings(g, 1).nodes.some(isGroup)).toBe(false);
  });

  it("leaves an expanded group open", () => {
    const folded = groupSiblings(fanOut(4), 3);
    const id = folded.nodes.find(isGroup)!.id;
    const open = groupSiblings(fanOut(4), 3, new Set([id]));
    expect(open.nodes.some(isGroup)).toBe(false);
  });

  it("gives a group its members' worst state, and orphans only if all are", () => {
    const g: TopologyGraph = {
      nodes: [
        node("bucket", "a", {
          status: { label: "Ready", tone: StatusTone.Success },
          orphan: true,
        }),
        node("bucket", "b", {
          status: { label: "Provisioning failed", tone: StatusTone.Danger },
        }),
      ],
      edges: [],
      columns: ["bucket"],
    };
    const [group] = groupSiblings(g, 1).nodes;
    expect(group.status?.tone).toBe(StatusTone.Danger);
    expect(group.orphan).toBe(false);
  });

  // Once a backend's buckets are one node, the tenants each bucket served
  // share it as a parent and fold too; a group never folds into another.
  it("folds again once parents are folded", () => {
    const buckets = ["1", "2", "3"].map((i) => node("bucket", i));
    const tenants = ["x", "y", "z"].map((i) => node("tenant", i));
    const g: TopologyGraph = {
      nodes: [node("backend", "be"), ...buckets, ...tenants],
      edges: [
        ...buckets.map((b) => ({ from: "backend:be", to: b.id })),
        ...buckets.map((b, i) => ({ from: b.id, to: tenants[i].id })),
      ],
      columns: ["backend", "bucket", "tenant"],
    };
    const out = groupSiblings(g, 2);
    expect(out.nodes.map((n) => n.label)).toEqual([
      "be",
      "3 buckets",
      "3 tenants",
    ]);
    expect(out.edges).toHaveLength(2);
  });
});
