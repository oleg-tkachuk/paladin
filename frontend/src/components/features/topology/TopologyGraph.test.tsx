import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

import type { TopologyGraph as Graph } from "@/lib/topology/model";

import { TopologyGraph } from "./TopologyGraph";

// One backend serving two buckets, one of which a tenant uses.
const GRAPH: Graph = {
  columns: ["backend", "bucket", "tenant"],
  nodes: [
    { id: "backend:be", kind: "backend", label: "primary", href: "/b/be" },
    { id: "bucket:a", kind: "bucket", label: "alpha", href: "/k/a" },
    { id: "bucket:b", kind: "bucket", label: "beta", href: "/k/b" },
    { id: "tenant:t", kind: "tenant", label: "acme", href: "/t/t" },
  ],
  edges: [
    { from: "backend:be", to: "bucket:a" },
    { from: "backend:be", to: "bucket:b" },
    { from: "bucket:a", to: "tenant:t" },
  ],
};

const node = (id: string) =>
  document.querySelector(`[data-node="${id}"]`) as HTMLElement;
const box = (label: string) =>
  screen.getByText(label).closest("a") as HTMLElement;
const lit = () => document.querySelectorAll("path[data-lit]");

describe("TopologyGraph highlighting", () => {
  it("lights a pointed node's parents, children and the curves to them", () => {
    render(<TopologyGraph graph={GRAPH} label="Topology" />);
    fireEvent.mouseEnter(node("bucket:a"));

    expect(node("bucket:a")).toHaveAttribute("data-active");
    expect(lit()).toHaveLength(2);
    // Its parent and child stay; its sibling recedes.
    expect(box("primary")).not.toHaveClass("opacity-40");
    expect(box("acme")).not.toHaveClass("opacity-40");
    expect(box("beta")).toHaveClass("opacity-40");
  });

  it("lets the graph go when the pointer leaves", () => {
    render(<TopologyGraph graph={GRAPH} label="Topology" />);
    fireEvent.mouseEnter(node("bucket:a"));
    fireEvent.mouseLeave(node("bucket:a"));

    expect(lit()).toHaveLength(0);
    expect(box("beta")).not.toHaveClass("opacity-40");
  });

  it("lights the same from the keyboard, on focus", () => {
    render(<TopologyGraph graph={GRAPH} label="Topology" />);
    fireEvent.focus(box("primary"));

    // The backend's two children, and nothing past them.
    expect(lit()).toHaveLength(2);
    expect(box("acme")).toHaveClass("opacity-40");
  });
});
