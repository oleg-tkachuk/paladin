import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ListLoadError } from "./ListLoadError";

// A failed read must read as a failure, in both shapes: announced (it replaces
// content a screen reader was told about), with the server's reason, and with
// a way to try again — never as an empty list.
describe.each(["block", "inline"] as const)("ListLoadError (%s)", (variant) => {
  it("announces the failure and says the list is unknown, not empty", () => {
    render(
      <ListLoadError
        what="Buckets"
        reason="unavailable: upstream connect error"
        onRetry={vi.fn()}
        variant={variant}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/Buckets could not be loaded/);
    expect(alert).toHaveTextContent(/unknown, not empty/);
    expect(alert).toHaveTextContent("unavailable: upstream connect error");
  });

  it("retries on request", async () => {
    const onRetry = vi.fn();
    render(
      <ListLoadError
        what="Buckets"
        reason="x"
        onRetry={onRetry}
        variant={variant}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
