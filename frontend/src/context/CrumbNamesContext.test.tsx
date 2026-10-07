import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import {
  CrumbNamesProvider,
  useCrumbNames,
  useNameCrumbs,
  type CrumbNames,
} from "./CrumbNamesContext";

const NAMES: CrumbNames = { "u-1": { label: "alice" } };

function Reader() {
  return <p>{useCrumbNames()["u-1"]?.label ?? "none"}</p>;
}

function Page() {
  useNameCrumbs(NAMES);
  return null;
}

describe("CrumbNamesContext", () => {
  it("holds a page's names while it is mounted, and drops them after", () => {
    const { rerender } = render(
      <CrumbNamesProvider>
        <Page />
        <Reader />
      </CrumbNamesProvider>,
    );
    expect(screen.getByText("alice")).toBeInTheDocument();
    rerender(
      <CrumbNamesProvider>
        <Reader />
      </CrumbNamesProvider>,
    );
    expect(screen.getByText("none")).toBeInTheDocument();
  });

  it("names nothing outside a provider", () => {
    render(
      <>
        <Page />
        <Reader />
      </>,
    );
    expect(screen.getByText("none")).toBeInTheDocument();
  });
});
