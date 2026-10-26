import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen } from "@/test/utils";

import { FACET_COUNTING } from "@/lib/table/facets";
import { StatusTone } from "@/lib/ui/status";

import { FacetFilter } from "./FacetFilter";

const OPTIONS = [
  { value: "on", label: "Enabled", tone: StatusTone.Success, count: 3 },
  { value: "off", label: "Disabled", count: 1 },
];

const open = (name: string) =>
  userEvent.click(screen.getByRole("button", { name }));

describe("FacetFilter", () => {
  it("lists every answer with its count and its status dot", async () => {
    render(
      <FacetFilter
        label="State"
        options={OPTIONS}
        selected={[]}
        onChange={() => {}}
      />,
    );
    await open("Filter by state");
    const on = screen.getByRole("menuitemcheckbox", {
      name: "Enabled, 3 rows",
    });
    expect(on).toHaveAttribute("aria-checked", "false");
    expect(on.querySelector("[data-slot=status-dot]")).toHaveAttribute(
      "data-tone",
      "success",
    );
    expect(
      screen.getByRole("menuitemcheckbox", { name: "Disabled, 1 row" }),
    ).toBeInTheDocument();
    // Nothing chosen, nothing to clear.
    expect(screen.getByRole("menuitem", { name: "Clear" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
  });

  it("adds an answer to the choice, and stays open for another", async () => {
    const onChange = vi.fn();
    render(
      <FacetFilter
        label="State"
        options={OPTIONS}
        selected={["on"]}
        onChange={onChange}
      />,
    );
    await open("Filter by state, 1 chosen");
    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Disabled/ }),
    );
    expect(onChange).toHaveBeenLastCalledWith(["on", "off"]);
    expect(screen.getByRole("menu")).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("menuitemcheckbox", { name: /Enabled/ }),
    );
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("clears the choice", async () => {
    const onChange = vi.fn();
    render(
      <FacetFilter
        label="State"
        options={OPTIONS}
        selected={["on", "off"]}
        onChange={onChange}
      />,
    );
    await open("Filter by state, 2 chosen");
    await userEvent.click(screen.getByRole("menuitem", { name: "Clear" }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("says when the rows give nothing to choose", async () => {
    render(
      <FacetFilter
        label="Label"
        options={[]}
        selected={[]}
        onChange={() => {}}
      />,
    );
    await open("Filter by label");
    expect(screen.getByText("Nothing to filter by")).toBeInTheDocument();
  });

  // A server that stopped counting at its scan cap gives lower bounds.
  it("marks every count a lower bound when the server's count stopped short", async () => {
    render(
      <FacetFilter
        label="State"
        options={[{ value: "on", label: "Enabled", count: 10_000 }]}
        selected={[]}
        onChange={() => {}}
        counting={FACET_COUNTING.lowerBound}
      />,
    );
    await open("Filter by state");
    const on = screen.getByRole("menuitemcheckbox", {
      name: "Enabled, at least 10,000 rows",
    });
    expect(on).toHaveTextContent(/^Enabled10k\+$/);
  });

  // Withheld is not nought: no count shows, and the answer is still chosen.
  it("offers the answers without a count when the server gave none", async () => {
    const onChange = vi.fn();
    render(
      <FacetFilter
        label="State"
        options={OPTIONS}
        selected={[]}
        onChange={onChange}
        counting={FACET_COUNTING.withheld}
      />,
    );
    await open("Filter by state");
    const on = screen.getByRole("menuitemcheckbox", { name: "Enabled" });
    expect(on).toHaveTextContent(/^Enabled$/);
    await userEvent.click(on);
    expect(onChange).toHaveBeenLastCalledWith(["on"]);
  });
});
