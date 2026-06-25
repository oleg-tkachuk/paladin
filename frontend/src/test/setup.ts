// jsdom test setup — loaded by the "dom" vitest project (see vitest.config.ts).
// Registers @testing-library/jest-dom matchers (toBeInTheDocument, …) and
// unmounts React trees after each test so state doesn't leak between cases
// (vitest does not auto-cleanup with globals:false).
import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});

// jsdom lacks a few browser APIs that Radix UI primitives (Select, dropdowns,
// dialogs) call during render/interaction. Stub them so component tests that
// mount Radix surfaces don't crash on the missing API.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
// jsdom ships requestSubmit as a stub that throws "Not implemented", and
// userEvent calls it when clicking a type="submit" button — so form onSubmit
// never fires. Override unconditionally to dispatch a cancelable submit event
// so React's onSubmit handler runs.
HTMLFormElement.prototype.requestSubmit = function (this: HTMLFormElement) {
  this.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
};
