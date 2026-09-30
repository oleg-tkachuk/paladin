import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@/test/utils";

const h = vi.hoisted(() => ({ issue: vi.fn(), showNotification: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  capabilityClient: { issue: h.issue },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { IssueCapabilityDialog } from "./IssueCapabilityDialog";

function open() {
  render(
    <IssueCapabilityDialog
      open
      tenantId="t-1"
      onClose={vi.fn()}
      onIssued={vi.fn()}
    />,
  );
}

describe("IssueCapabilityDialog", () => {
  beforeEach(() => {
    h.issue.mockReset();
    h.showNotification.mockReset();
  });

  // A limit left empty with Unlimited off would have been sent as 0, which
  // the protocol reads as "forbid"; it was caught only by a toast on submit.
  it("holds the submit while a limit is on but empty, and says so", () => {
    open();
    fireEvent.change(screen.getByLabelText(/Subject/), {
      target: { value: "agent-7" },
    });
    expect(
      screen.getByRole("button", { name: "Issue capability" }),
    ).toBeEnabled();

    // The Unlimited toggle beside the Max requests input.
    const maxRequests = screen.getByLabelText("Max requests");
    fireEvent.click(
      maxRequests.parentElement!.querySelector("button[aria-pressed]")!,
    );
    expect(
      screen.getByText("Set max requests, or make it Unlimited."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Issue capability" }),
    ).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Max requests"), {
      target: { value: "-3" },
    });
    expect(screen.getByLabelText("Max requests")).toHaveAccessibleDescription(
      "A positive number, or Unlimited.",
    );
  });
});
