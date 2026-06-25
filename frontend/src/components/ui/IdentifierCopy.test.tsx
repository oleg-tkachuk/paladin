import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// IdentifierCopy pulls showNotification from the Notification context; the
// component under test only needs it to be callable, so stub the hook.
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import { IdentifierCopy } from "./IdentifierCopy";

const writeText = vi.fn().mockResolvedValue(undefined);

beforeEach(() => {
  writeText.mockClear();
  // jsdom has no clipboard; install a stub.
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText },
    configurable: true,
  });
});

describe("IdentifierCopy", () => {
  it("renders the label and value", () => {
    render(<IdentifierCopy value="tnt_abc123" label="Tenant ID" />);
    expect(screen.getByText("Tenant ID")).toBeInTheDocument();
    expect(screen.getByText("tnt_abc123")).toBeInTheDocument();
  });

  it("copies the value to the clipboard when the icon button is clicked", async () => {
    render(<IdentifierCopy value="tnt_abc123" label="Tenant ID" iconOnly />);
    await userEvent.click(screen.getByRole("button"));
    expect(writeText).toHaveBeenCalledWith("tnt_abc123");
  });

  it("copies when the full (non-iconOnly) surface is clicked", async () => {
    render(<IdentifierCopy value="key/path.txt" label="Object key" />);
    await userEvent.click(screen.getByText("key/path.txt"));
    expect(writeText).toHaveBeenCalledWith("key/path.txt");
  });
});
