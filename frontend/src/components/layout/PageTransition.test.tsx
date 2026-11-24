import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const seen = vi.hoisted(() => ({ classes: [] as unknown[] }));
// React's boundary renders nothing of its own; the class it is given is the
// whole of this component's decision.
vi.mock("react", async (actual) => {
  const react = await actual<typeof import("react")>();
  return {
    ...react,
    ViewTransition: ({
      default: cls,
      children,
    }: {
      default: unknown;
      children: React.ReactNode;
    }) => {
      seen.classes.push(cls);
      return children;
    },
  };
});

import { PAGE_TRANSITION_CLASS, REDUCED_MOTION_QUERY } from "@/lib/ui/motion";

import { PageTransition } from "./PageTransition";

function setUp({ api, reduced }: { api: boolean; reduced: boolean }) {
  Object.defineProperty(document, "startViewTransition", {
    configurable: true,
    value: api ? () => {} : undefined,
  });
  window.matchMedia = vi.fn((q: string) => ({
    matches: q === REDUCED_MOTION_QUERY && reduced,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })) as unknown as typeof window.matchMedia;
}

afterEach(() => {
  seen.classes = [];
});

describe("PageTransition", () => {
  it.each([
    [
      "the browser runs them and motion is wanted",
      true,
      false,
      PAGE_TRANSITION_CLASS,
    ],
    ["the user asked for less motion", true, true, "none"],
    ["the browser cannot run them", false, false, "none"],
  ])("when %s, the page changes under %s", (_, api, reduced, want) => {
    setUp({ api, reduced });
    render(
      <PageTransition>
        <p>page</p>
      </PageTransition>,
    );
    expect(screen.getByText("page")).toBeInTheDocument();
    expect(seen.classes.at(-1)).toBe(want);
  });
});
