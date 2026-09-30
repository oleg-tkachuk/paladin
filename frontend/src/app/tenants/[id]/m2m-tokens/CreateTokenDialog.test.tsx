import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({ create: vi.fn(), showNotification: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  apiTokenClient: { create: h.create },
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { CreateTokenDialog } from "./CreateTokenDialog";

function open() {
  render(
    <CreateTokenDialog
      open
      tenantId="t-1"
      onClose={vi.fn()}
      onCreated={vi.fn()}
    />,
  );
}

describe("CreateTokenDialog", () => {
  beforeEach(() => {
    h.create.mockReset();
    h.showNotification.mockReset();
  });

  it("says what the held submit is waiting for", () => {
    open();
    expect(screen.getByText("Enter a name to continue.")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Name/), {
      target: { value: "ci" },
    });
    fireEvent.change(screen.getByLabelText("Scopes"), {
      target: { value: "nonsense" },
    });
    expect(screen.getByText("Fix the scopes to continue.")).toBeInTheDocument();
    expect(screen.getByLabelText("Scopes")).toHaveAccessibleDescription(
      "Not a scope: nonsense.",
    );
  });

  it("keeps a failed create in the dialog, with the reason", async () => {
    h.create.mockRejectedValue(new Error("boom"));
    open();
    fireEvent.change(screen.getByLabelText(/Name/), {
      target: { value: "ci" },
    });
    fireEvent.submit(
      screen.getByRole("button", { name: "Create token" }).closest("form")!,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("Create failed");
  });
});
