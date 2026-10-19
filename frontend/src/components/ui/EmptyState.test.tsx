import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ArchiveBoxIcon } from "@heroicons/react/24/outline";

import { render, screen } from "@/test/utils";
import { EmptyState } from "./EmptyState";

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
});
