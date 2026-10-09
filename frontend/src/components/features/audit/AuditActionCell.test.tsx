import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AuditActionCell } from "./AuditActionCell";

const PURGE = "/paladin.admin.v1.TenantService/PurgeTenant";
const DELETE = "/paladin.admin.v1.TenantService/DeleteTenant";

describe("AuditActionCell", () => {
  it("shows the method, where it ran, and the full action on hover", () => {
    const { container } = render(<AuditActionCell action={DELETE} />);
    expect(screen.getByText("DeleteTenant")).toBeInTheDocument();
    expect(screen.getByText("admin · TenantService")).toBeInTheDocument();
    expect(container.firstElementChild).toHaveAttribute("title", DELETE);
  });

  it("marks a successful delete by its verb, not as a failure", () => {
    const { container } = render(<AuditActionCell action={DELETE} />);
    expect(container.querySelector("[data-kind]")).toHaveAttribute(
      "data-kind",
      "destroy",
    );
    expect(screen.queryByText("failed")).toBeNull();
  });

  it("shows a failure's code and its message", () => {
    render(
      <AuditActionCell
        action={PURGE}
        errorMessage="failed_precondition: tenant still has rows"
      />,
    );
    expect(screen.getByText("failed_precondition")).toBeInTheDocument();
    expect(screen.getByText("tenant still has rows")).toBeInTheDocument();
  });

  it("labels a failure without a code as failed", () => {
    render(<AuditActionCell action={PURGE} errorMessage="boom" />);
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText("boom")).toBeInTheDocument();
  });
});
