import { describe, expect, it } from "vitest";

import { isPlainClick, type ClickLike } from "./plainClick";

const click = (over: Partial<ClickLike> = {}): ClickLike => ({
  button: 0,
  metaKey: false,
  ctrlKey: false,
  shiftKey: false,
  altKey: false,
  ...over,
});

describe("isPlainClick", () => {
  it("is a primary click with no modifier", () => {
    expect(isPlainClick(click())).toBe(true);
  });

  const cases: [string, Partial<ClickLike>][] = [
    ["the middle button", { button: 1 }],
    ["Cmd", { metaKey: true }],
    ["Ctrl", { ctrlKey: true }],
    ["Shift", { shiftKey: true }],
    ["Alt", { altKey: true }],
  ];
  for (const [name, over] of cases) {
    it(`is not with ${name}`, () => {
      expect(isPlainClick(click(over))).toBe(false);
    });
  }
});
