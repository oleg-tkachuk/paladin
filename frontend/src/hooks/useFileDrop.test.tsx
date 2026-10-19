import { describe, expect, it, vi } from "vitest";

import { fireEvent, render, screen } from "@/test/utils";
import { carriesFiles, useFileDrop } from "./useFileDrop";

function Zone({
  onFiles,
  disabled,
}: {
  onFiles: (f: File[]) => void;
  disabled?: boolean;
}) {
  const { dragging, dropProps } = useFileDrop({ onFiles, disabled });
  return (
    <div data-testid="zone" data-dragging={dragging} {...dropProps}>
      <span data-testid="child">child</span>
    </div>
  );
}

const file = new File(["x"], "a.txt", { type: "text/plain" });
const withFiles = { dataTransfer: { types: ["Files"], files: [file] } };
const withText = { dataTransfer: { types: ["text/plain"], files: [] } };

describe("carriesFiles", () => {
  it("tells files from other drags", () => {
    expect(carriesFiles(["Files"])).toBe(true);
    expect(carriesFiles(["text/plain", "text/uri-list"])).toBe(false);
  });
});

describe("useFileDrop", () => {
  it("stays over the region while the drag moves onto its children", () => {
    render(<Zone onFiles={vi.fn()} />);
    const zone = screen.getByTestId("zone");
    fireEvent.dragEnter(zone, withFiles);
    fireEvent.dragEnter(screen.getByTestId("child"), withFiles);
    fireEvent.dragLeave(zone, withFiles);
    expect(zone).toHaveAttribute("data-dragging", "true");
    fireEvent.dragLeave(screen.getByTestId("child"), withFiles);
    expect(zone).toHaveAttribute("data-dragging", "false");
  });

  it("hands over the dropped files and stops showing the drag", () => {
    const onFiles = vi.fn();
    render(<Zone onFiles={onFiles} />);
    const zone = screen.getByTestId("zone");
    fireEvent.dragEnter(zone, withFiles);
    fireEvent.drop(zone, withFiles);
    expect(onFiles).toHaveBeenCalledWith([file]);
    expect(zone).toHaveAttribute("data-dragging", "false");
  });

  it("leaves a drag of text alone", () => {
    const onFiles = vi.fn();
    render(<Zone onFiles={onFiles} />);
    const zone = screen.getByTestId("zone");
    fireEvent.dragEnter(zone, withText);
    fireEvent.drop(zone, withText);
    expect(zone).toHaveAttribute("data-dragging", "false");
    expect(onFiles).not.toHaveBeenCalled();
  });

  it("refuses a drop while disabled", () => {
    const onFiles = vi.fn();
    render(<Zone onFiles={onFiles} disabled />);
    const zone = screen.getByTestId("zone");
    fireEvent.dragEnter(zone, withFiles);
    fireEvent.drop(zone, withFiles);
    expect(zone).toHaveAttribute("data-dragging", "false");
    expect(onFiles).not.toHaveBeenCalled();
  });
});
