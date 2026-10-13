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

  it("does not offer the admin plane, which a roleless token cannot reach", () => {
    open();
    const planes = screen.getByRole("group", { name: "Planes" });
    expect(planes).toHaveTextContent("data");
    expect(planes).not.toHaveTextContent("admin");
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
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });

  // "No cap" is its own value, never a 0 the server reads as "admits no
  // request"; a typed 0 is that cap.
  describe("rate limit", () => {
    const toggle = () =>
      screen
        .getByLabelText(/Rate limit/)
        .parentElement!.querySelector("button[aria-pressed]") as HTMLElement;
    const submit = () =>
      fireEvent.submit(
        screen.getByRole("button", { name: "Create token" }).closest("form")!,
      );
    const named = () =>
      fireEvent.change(screen.getByLabelText(/Name/), {
        target: { value: "ci" },
      });

    it("creates an uncapped token by default", () => {
      h.create.mockReturnValue(new Promise(() => {}));
      open();
      named();
      submit();
      expect(h.create.mock.calls[0][0].rateLimit).toEqual({
        case: "unlimitedRate",
        value: true,
      });
    });

    it("sends a typed 0 as a cap", () => {
      h.create.mockReturnValue(new Promise(() => {}));
      open();
      named();
      fireEvent.click(toggle());
      fireEvent.change(screen.getByLabelText(/Rate limit/), {
        target: { value: "0" },
      });
      submit();
      expect(h.create.mock.calls[0][0].rateLimit).toEqual({
        case: "rateLimitRpm",
        value: 0,
      });
    });

    it("holds the submit while the cap is on but empty", () => {
      open();
      named();
      fireEvent.click(toggle());
      expect(
        screen.getByText("Set the rate limit, or make it Unlimited."),
      ).toBeInTheDocument();
      submit();
      expect(h.create).not.toHaveBeenCalled();
    });
  });
});
