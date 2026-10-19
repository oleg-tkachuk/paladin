import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ setShown: vi.fn(async () => {}), shown: false }));
vi.mock("@/hooks/useResourceLinks", () => ({
  useResourceLinks: () => ({ shown: h.shown, setShown: h.setShown }),
}));

import { ResourceLinksField } from "./ResourceLinksField";

// The way back once the section was hidden from the sidebar's menu.
describe("ResourceLinksField", () => {
  it("turns the sidebar's section back on", async () => {
    render(<ResourceLinksField />);
    const toggle = screen.getByRole("switch");
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    expect(h.setShown).toHaveBeenCalledWith(true);
  });
});
