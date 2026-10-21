import { describe, expect, it } from "vitest";
import { render, screen } from "@/test/utils";

import { ClientCell } from "./ClientCell";

describe("ClientCell", () => {
  it("names the application, and the SDK beneath it", () => {
    const ua = "paladin-sdk-go/0.97.0 gateway/1.4";
    render(<ClientCell userAgent={ua} />);
    expect(screen.getByText("gateway/1.4")).toBeInTheDocument();
    expect(screen.getByText("Go SDK 0.97.0")).toBeInTheDocument();
    expect(screen.getByTitle(ua)).toBeInTheDocument();
  });

  it("shows a dash for a call that named no client", () => {
    render(<ClientCell userAgent="" />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});
