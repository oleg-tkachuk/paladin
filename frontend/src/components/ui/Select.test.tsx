import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

import { Select } from "./Select";

describe("legacy Select", () => {
  it("puts id and aria-label on the combobox", () => {
    render(
      <>
        <label htmlFor="unit">Unit</label>
        <Select
          id="unit"
          options={[{ value: "a", label: "A" }]}
          value="a"
          onChange={vi.fn()}
        />
        <Select
          aria-label="Granularity"
          options={[{ value: "b", label: "B" }]}
          value="b"
          onChange={vi.fn()}
        />
      </>,
    );
    expect(screen.getByRole("combobox", { name: "Unit" })).toBeInTheDocument();
    expect(
      screen.getByRole("combobox", { name: "Granularity" }),
    ).toBeInTheDocument();
  });

  // What a FormField hands its control: the error below the field is the
  // trigger's description, so a screen reader hears why it is refused.
  it("takes a field's note as its description and its invalid state", () => {
    render(
      <>
        <Select
          aria-label="Collection"
          aria-describedby="collection-note"
          aria-invalid
          options={[{ value: "a", label: "A" }]}
          onChange={vi.fn()}
        />
        <p id="collection-note">Choose a collection.</p>
      </>,
    );
    const trigger = screen.getByRole("combobox", { name: "Collection" });
    expect(trigger).toHaveAccessibleDescription("Choose a collection.");
    expect(trigger).toHaveAttribute("aria-invalid", "true");
  });
});

// A select with neither is announced by its current value alone — "All
// backends" says nothing about what it filters. Radix renders the trigger as
// a button, so a <Label htmlFor> reaches it only through an id on the trigger.
const SRC = join(__dirname, "..", "..");
const OPENING_TAG = /<(SelectTrigger|Select)\b([^>]*?)(\/?)>/gs;
// `{...control}` is what a FormField hands its control: the id its label
// points at.
const LABELLED = /\bid=|\baria-label=|\baria-labelledby=|\{\.\.\.control\}/;

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory())
      return name === "gen" ? [] : sources(path);
    return path.endsWith(".tsx") && !path.endsWith(".test.tsx") ? [path] : [];
  });
}

describe("every select in the console", () => {
  it("has a label", () => {
    const unlabelled: string[] = [];
    for (const file of sources(SRC)) {
      if (file.endsWith(join("components", "ui", "Select.tsx"))) continue;
      const text = readFileSync(file, "utf8");
      for (const m of text.matchAll(OPENING_TAG)) {
        if (!LABELLED.test(m[2])) {
          const line = text.slice(0, m.index).split("\n").length;
          unlabelled.push(`${relative(SRC, file)}:${line}`);
        }
      }
    }
    expect(unlabelled).toEqual([]);
  });
});
