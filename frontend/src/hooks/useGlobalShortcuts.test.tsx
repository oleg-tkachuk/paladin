import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

import { NAV_CHORD_WINDOW_MS, NAV_PREFIX } from "@/lib/navShortcuts";

const h = vi.hoisted(() => ({
  push: vi.fn(),
  tenant: undefined as { slug?: string; tenantId?: string } | undefined,
}));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: h.push }) }));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenant: h.tenant }),
}));

import { useGlobalShortcuts } from "./useGlobalShortcuts";

function Harness() {
  useGlobalShortcuts();
  return (
    <>
      <input aria-label="field" />
      <textarea aria-label="area" />
      <select aria-label="choice" />
    </>
  );
}

const press = (key: string, init: KeyboardEventInit = {}, target?: Element) =>
  fireEvent.keyDown(target ?? document, { key, ...init });

describe("useGlobalShortcuts", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    h.push.mockClear();
    h.tenant = undefined;
  });
  afterEach(() => vi.useRealTimers());

  it("jumps on the prefix followed by a key", () => {
    render(<Harness />);
    press(NAV_PREFIX);
    press("b");
    expect(h.push).toHaveBeenCalledExactlyOnceWith("/buckets");
  });

  it("goes to the scoped tenant's page while one is in scope", () => {
    h.tenant = { slug: "acme", tenantId: "u-1" };
    render(<Harness />);
    press(NAV_PREFIX);
    press("b");
    expect(h.push).toHaveBeenCalledExactlyOnceWith("/tenants/acme/buckets");
  });

  it("does nothing for a key without the prefix", () => {
    render(<Harness />);
    press("b");
    expect(h.push).not.toHaveBeenCalled();
  });

  it("lets the chord expire after the window", () => {
    render(<Harness />);
    press(NAV_PREFIX);
    vi.advanceTimersByTime(NAV_CHORD_WINDOW_MS);
    press("b");
    expect(h.push).not.toHaveBeenCalled();
  });

  it("re-arms on a second prefix rather than jumping", () => {
    render(<Harness />);
    press(NAV_PREFIX);
    press(NAV_PREFIX);
    expect(h.push).not.toHaveBeenCalled();
    press("d");
    expect(h.push).toHaveBeenCalledExactlyOnceWith("/");
  });

  it("disarms on a key that is not a jump", () => {
    render(<Harness />);
    press(NAV_PREFIX);
    press("x");
    press("b");
    expect(h.push).not.toHaveBeenCalled();
  });

  it("is not armed by the shifted prefix", () => {
    render(<Harness />);
    press(NAV_PREFIX.toUpperCase(), { shiftKey: true });
    press("b");
    expect(h.push).not.toHaveBeenCalled();
  });

  it.each(["metaKey", "ctrlKey", "altKey"])(
    "ignores the chord with %s held",
    (modifier) => {
      render(<Harness />);
      press(NAV_PREFIX);
      press("b", { [modifier]: true });
      expect(h.push).not.toHaveBeenCalled();
    },
  );

  it.each(["field", "area", "choice"])("is inert in the %s", (label) => {
    render(<Harness />);
    const el = screen.getByLabelText(label);
    press(NAV_PREFIX, {}, el);
    press("b", {}, el);
    expect(h.push).not.toHaveBeenCalled();
  });

  it("is inert in a contentEditable element", () => {
    render(<Harness />);
    const el = document.createElement("div");
    // jsdom does not implement isContentEditable; a browser derives it
    // from contentEditable="true".
    Object.defineProperty(el, "isContentEditable", { value: true });
    document.body.appendChild(el);
    press(NAV_PREFIX, {}, el);
    press("b", {}, el);
    expect(h.push).not.toHaveBeenCalled();
    el.remove();
  });
});
