import { describe, expect, it } from "vitest";

import { render } from "@/test/utils";
import { CHORD_KEY_SEPARATOR } from "@/lib/navShortcuts";

import { KeyChord } from "./KeyChord";

describe("KeyChord", () => {
  it("shows each key of the chord in its own cap, hidden from readers", () => {
    const { container } = render(
      <KeyChord chord={["G", "B"].join(CHORD_KEY_SEPARATOR)} />,
    );
    const caps = [...container.querySelectorAll("kbd")].map(
      (k) => k.textContent,
    );
    expect(caps).toEqual(["G", "B"]);
    expect(container.firstElementChild).toHaveAttribute("aria-hidden", "true");
  });
});
