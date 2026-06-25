import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { EmptyState } from "./EmptyState";

describe("EmptyState", () => {
  it("renders the title and description", () => {
    render(<EmptyState title="Nothing here" description="Add one to begin" />);
    expect(screen.getByText("Nothing here")).toBeInTheDocument();
    expect(screen.getByText("Add one to begin")).toBeInTheDocument();
  });

  it("renders an action link when actionHref is given", () => {
    render(
      <EmptyState title="Empty" actionLabel="Create" actionHref="/tenants" />,
    );
    const link = screen.getByRole("link", { name: "Create" });
    expect(link).toHaveAttribute("href", "/tenants");
  });

  it("calls onAction when the action button is clicked", async () => {
    const onAction = vi.fn();
    render(
      <EmptyState title="Empty" actionLabel="Refresh" onAction={onAction} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(onAction).toHaveBeenCalledOnce();
  });

  it("prefers the link over the button when both href and onAction are given", () => {
    render(
      <EmptyState
        title="Empty"
        actionLabel="Go"
        actionHref="/x"
        onAction={vi.fn()}
      />,
    );
    expect(screen.getByRole("link", { name: "Go" })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Go" }),
    ).not.toBeInTheDocument();
  });

  it("marks the decorative icon aria-hidden so it is not announced", () => {
    const { container } = render(<EmptyState title="Empty" />);
    expect(container.querySelector('[aria-hidden="true"]')).toBeTruthy();
  });
});
