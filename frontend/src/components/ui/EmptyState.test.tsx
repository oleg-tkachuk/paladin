import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ArchiveBoxIcon } from "@heroicons/react/24/outline";

import { render, screen } from "@/test/utils";
import { EmptyState, EmptyTableRow } from "./EmptyState";

const action = (onClick = vi.fn()) => ({
  label: "Create the first bucket",
  onClick,
});

describe("EmptyState", () => {
  it("offers the first one, and says what it is for, when there are none", async () => {
    const a = action();
    render(
      <EmptyState
        icon={ArchiveBoxIcon}
        what="buckets"
        reason="empty"
        purpose="A bucket is where a collection's objects are stored."
        action={a}
      />,
    );
    expect(screen.getByText("No buckets yet.")).toBeInTheDocument();
    expect(
      screen.getByText(/where a collection's objects/),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /Create the first bucket/ }),
    );
    expect(a.onClick).toHaveBeenCalled();
  });

  it.each(["filtered", "pending"] as const)(
    "offers no create while %s: the rows may exist",
    (reason) => {
      render(
        <EmptyState
          icon={ArchiveBoxIcon}
          what="buckets"
          reason={reason}
          purpose="x"
          action={action()}
        />,
      );
      expect(screen.queryByRole("button")).toBeNull();
      expect(screen.queryByText("x")).toBeNull();
    },
  );

  it("offers to clear the filters when they hid every row", async () => {
    const onClearFilters = vi.fn();
    render(
      <EmptyState
        icon={ArchiveBoxIcon}
        what="buckets"
        reason="filtered"
        action={action()}
        onClearFilters={onClearFilters}
      />,
    );
    expect(
      screen.getByText("No buckets match your filters."),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Clear filters" }),
    );
    expect(onClearFilters).toHaveBeenCalled();
  });

  it.each(["empty", "pending"] as const)(
    "offers no clear while %s: nothing is hidden yet",
    (reason) => {
      render(
        <EmptyState
          icon={ArchiveBoxIcon}
          what="buckets"
          reason={reason}
          onClearFilters={vi.fn()}
        />,
      );
      expect(
        screen.queryByRole("button", { name: "Clear filters" }),
      ).toBeNull();
    },
  );

  // A table cell keeps its text on one line, so a long purpose ran past the
  // table's card; the empty row lets its words wrap. jsdom lays nothing out,
  // so the class that does it is what can be held.
  it("lets an empty table row's words wrap", () => {
    render(
      <table>
        <tbody>
          <EmptyTableRow
            colSpan={3}
            icon={ArchiveBoxIcon}
            what="capabilities"
            reason="empty"
            purpose="An agent you connect through OAuth gets one here each time it exchanges its token."
          />
        </tbody>
      </table>,
    );
    expect(screen.getByRole("cell").className).toContain("whitespace-normal");
  });
});
