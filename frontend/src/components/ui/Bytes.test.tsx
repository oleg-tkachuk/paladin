import { describe, expect, it } from "vitest";

import { render, screen } from "@/test/utils";

import { Bytes } from "./Bytes";

describe("Bytes", () => {
  it("shows the figure, its unit muted, and every byte on hover", () => {
    render(<Bytes value={1536} />);
    const size = screen.getByTitle("1,536 bytes");
    expect(size).toHaveTextContent("1.5 KB");
    expect(size).toHaveClass("tabular-nums");
    const unit = screen.getByText("KB", { exact: false });
    expect(unit).toHaveClass("text-muted-foreground");
    expect(unit).not.toHaveTextContent("1.5");
  });
});
