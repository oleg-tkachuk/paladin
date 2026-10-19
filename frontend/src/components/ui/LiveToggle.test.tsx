import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { render, screen } from "@/test/utils";
import { LiveToggle } from "./LiveToggle";

describe("LiveToggle", () => {
  it("keeps its name and says by aria-pressed which way it is", async () => {
    const onToggle = vi.fn();
    const { rerender } = render(
      <LiveToggle state="poll" onToggle={onToggle} intervalMs={15_000} />,
    );
    const live = screen.getByRole("button", { name: "Live" });
    expect(live).toHaveAttribute("aria-pressed", "true");
    expect(live).toHaveAttribute("title", expect.stringMatching(/every 15 s/));
    await userEvent.click(live);
    expect(onToggle).toHaveBeenCalledTimes(1);

    rerender(<LiveToggle state="paused" onToggle={onToggle} />);
    const paused = screen.getByRole("button", { name: "Live" });
    expect(paused).toHaveAttribute("aria-pressed", "false");
    expect(paused).toHaveTextContent("Paused");
  });
});
