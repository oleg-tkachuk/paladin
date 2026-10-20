import { describe, expect, it } from "vitest";

import { StatusTone } from "@/lib/ui/status";

import {
  neighbourhood,
  nodeMatches,
  worstStatus,
  type TopologyNode,
} from "./model";

const ok = { label: "Ready", tone: StatusTone.Success };
const failed = { label: "Probe failed", tone: StatusTone.Danger };
const untested = { label: "Untested", tone: StatusTone.Unknown };

describe("worstStatus", () => {
  it("picks what most needs a look", () => {
    expect(worstStatus([ok, failed, untested])).toBe(failed);
    expect(worstStatus([ok, untested])).toBe(untested);
  });

  it("keeps the first of equals, so the result follows the input", () => {
    const other = { label: "Also ready", tone: StatusTone.Success };
    expect(worstStatus([ok, other])).toBe(ok);
  });

  it("is undefined when nothing has a state", () => {
    expect(worstStatus([undefined, undefined])).toBeUndefined();
    expect(worstStatus([])).toBeUndefined();
  });
});

describe("nodeMatches", () => {
  const bucket: TopologyNode = {
    id: "bucket:be/media",
    kind: "bucket",
    label: "media",
    detail: "Granted · Default bucket",
    status: { label: "Provisioning failed", tone: StatusTone.Danger },
  };

  it.each([
    ["", true],
    ["  ", true],
    ["MED", true],
    ["default", true],
    ["bucket", true],
    ["failed", true],
    ["tenant", false],
  ])("%j → %s", (q, want) => {
    expect(nodeMatches(bucket, q)).toBe(want);
  });

  // A collapsed group would otherwise hide the very node a filter was for.
  it("matches a group by its members", () => {
    const group: TopologyNode = {
      id: "group:bucket|",
      kind: "bucket",
      label: "2 buckets",
      members: [bucket, { ...bucket, id: "bucket:be/logs", label: "logs" }],
    };
    expect(nodeMatches(group, "logs")).toBe(true);
    expect(nodeMatches(group, "2 buckets")).toBe(false);
  });
});

describe("neighbourhood", () => {
  // backend → bucket → tenant, and a second bucket on the same backend.
  const graph = {
    edges: [
      { from: "backend:be", to: "bucket:a" },
      { from: "backend:be", to: "bucket:b" },
      { from: "bucket:a", to: "tenant:t" },
    ],
  };

  it("takes a node's parents and children, and the edges to them", () => {
    const n = neighbourhood(graph, "bucket:a");
    expect([...n.nodes].sort()).toEqual(["backend:be", "bucket:a", "tenant:t"]);
    expect([...n.edges].sort()).toEqual([
      "backend:be>bucket:a",
      "bucket:a>tenant:t",
    ]);
  });

  // A sibling shares a parent; it is not one edge away.
  it("leaves out siblings and what lies two edges off", () => {
    const n = neighbourhood(graph, "bucket:a");
    expect(n.nodes.has("bucket:b")).toBe(false);
    expect([...neighbourhood(graph, "tenant:t").nodes].sort()).toEqual([
      "bucket:a",
      "tenant:t",
    ]);
  });

  it("is the node alone when nothing joins it", () => {
    const n = neighbourhood(graph, "bucket:lone");
    expect([...n.nodes]).toEqual(["bucket:lone"]);
    expect(n.edges.size).toBe(0);
  });
});
