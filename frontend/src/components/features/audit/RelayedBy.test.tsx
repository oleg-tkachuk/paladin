import { describe, expect, it } from "vitest";
import { render, screen } from "@/test/utils";

import { RELAYED_BY_LABEL, RelayedBy } from "./RelayedBy";

describe("RelayedBy", () => {
  it("names the client that relayed the call", () => {
    render(<RelayedBy client="paladin-mcp-server" />);
    expect(
      screen.getByText(`${RELAYED_BY_LABEL} paladin-mcp-server`),
    ).toBeInTheDocument();
  });

  it("says nothing about a direct call", () => {
    const { container } = render(<RelayedBy client="" />);
    expect(container).toBeEmptyDOMElement();
  });
});
