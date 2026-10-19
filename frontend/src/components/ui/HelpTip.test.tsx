import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { render, screen } from "@/test/utils";
import { HELP, helpHref } from "@/lib/help";
import { HelpTip } from "./HelpTip";

describe("HelpTip", () => {
  it("opens the term's explanation with a link to its document", async () => {
    render(<HelpTip topic="publicRead" />);
    await userEvent.click(
      screen.getByRole("button", { name: "What is public read?" }),
    );
    expect(await screen.findByText(HELP.publicRead.text)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Read ADR-0027" })).toHaveAttribute(
      "href",
      helpHref("publicRead"),
    );
  });
});
