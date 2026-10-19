import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { COPIED_NOTICE, IdentifierCopy } from "./IdentifierCopy";
import { CopyableId } from "./CopyableId";

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

  // It was a clickable div: no focus, no key press, so a keyboard could not
  // copy the identifier at all.
  it("copies from the keyboard", async () => {
    render(<IdentifierCopy value="cap_01" label="Capability ID" />);
    const button = screen.getByRole("button", { name: /Capability ID/ });
    await userEvent.tab();
    expect(button).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(writeText).toHaveBeenCalledWith("cap_01");
  });
});

// A copy is confirmed where it happened: the button names itself, and its
// live region says so — no toast, and no notification context needed.
describe("IdentifierCopy confirmation", () => {
  it("is named, and says it copied", async () => {
    render(<IdentifierCopy value="tnt_abc123" label="tenant ID" iconOnly />);
    await userEvent.click(
      screen.getByRole("button", { name: "Copy tenant ID" }),
    );
    expect(await screen.findByText(COPIED_NOTICE)).toBeInTheDocument();
  });
});

describe("CopyableId", () => {
  it("shows the value and copies it", async () => {
    render(<CopyableId value="01a0f4a9" label="user ID" />);
    expect(screen.getByText("01a0f4a9")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Copy user ID" }));
    expect(writeText).toHaveBeenCalledWith("01a0f4a9");
  });
});
