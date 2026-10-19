// @vitest-environment jsdom
import { describe, expect, it } from "vitest";

import { labelCells } from "./cellLabels";

function table(html: string): HTMLTableElement {
  const t = document.createElement("table");
  t.innerHTML = html;
  return t;
}

describe("labelCells", () => {
  it("names each cell by its column, counting spans", () => {
    const t = table(`
      <thead><tr><th></th><th>Name</th><th colspan="2">Size</th><th><span>Actions</span></th></tr></thead>
      <tbody><tr><td>☐</td><td>a.txt</td><td colspan="2">4 KB</td><td>…</td></tr></tbody>`);
    labelCells(t);
    const cells = Array.from(t.tBodies[0].rows[0].cells);
    expect(cells.map((c) => c.dataset.label)).toEqual([
      undefined,
      "Name",
      "Size",
      "Actions",
    ]);
  });

  it("leaves a row that spans the table alone", () => {
    const t = table(`
      <thead><tr><th>Name</th><th>Size</th></tr></thead>
      <tbody><tr><td colspan="2">No objects yet.</td></tr></tbody>`);
    labelCells(t);
    expect(t.tBodies[0].rows[0].cells[0].dataset.label).toBeUndefined();
  });

  it("prefers a header's accessible label", () => {
    const t = table(`
      <thead><tr><th aria-label="Select">☐</th></tr></thead>
      <tbody><tr><td>x</td></tr></tbody>`);
    labelCells(t);
    expect(t.tBodies[0].rows[0].cells[0].dataset.label).toBe("Select");
  });
});
