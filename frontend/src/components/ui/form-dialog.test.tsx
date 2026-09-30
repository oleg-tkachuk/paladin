import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

import { Input } from "./input";
import { FormDialog, FormDisclosure, FormField } from "./form-dialog";

function dialog(props: Partial<React.ComponentProps<typeof FormDialog>> = {}) {
  const onSubmit = vi.fn();
  render(
    <FormDialog
      open
      onOpenChange={vi.fn()}
      title="New thing"
      submitLabel="Create thing"
      onSubmit={onSubmit}
      {...props}
    >
      <FormField label="Name" required hint="Lowercase.">
        {(control) => <Input {...control} />}
      </FormField>
    </FormDialog>,
  );
  return onSubmit;
}

describe("FormField", () => {
  it("ties the label and the hint to the control", () => {
    dialog();
    const input = screen.getByLabelText(/Name/);
    expect(input).toHaveAccessibleDescription("Lowercase.");
  });

  it("shows the error in place of the hint and marks the control", () => {
    render(
      <FormField label="Name" hint="Lowercase." error="Too short.">
        {(control) => <Input {...control} />}
      </FormField>,
    );
    const input = screen.getByLabelText("Name");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Too short.");
    expect(screen.queryByText("Lowercase.")).toBeNull();
  });
});

describe("FormDialog", () => {
  it("submits on Enter when nothing holds it", () => {
    const onSubmit = dialog();
    fireEvent.submit(screen.getByLabelText(/Name/).closest("form")!);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  // A held submit used to be a grey button and nothing else.
  it("says why the submit is held, and does not submit", () => {
    const onSubmit = dialog({ blockedReason: "Pick a bucket to continue." });
    expect(screen.getByText("Pick a bucket to continue.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create thing" })).toBeDisabled();
    fireEvent.submit(screen.getByLabelText(/Name/).closest("form")!);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("names the pending action while submitting", () => {
    dialog({ submitting: true, submittingLabel: "Creating…" });
    expect(screen.getByRole("button", { name: "Creating…" })).toBeDisabled();
  });

  it("shows a submit failure as an alert", () => {
    dialog({ error: "slug already taken" });
    expect(screen.getByRole("alert")).toHaveTextContent("slug already taken");
  });
});

describe("FormDisclosure", () => {
  it("keeps its fields collapsed until opened", () => {
    render(
      <FormDisclosure title="Advanced">
        <span>secret field</span>
      </FormDisclosure>,
    );
    const toggle = screen.getByRole("button", { name: "Advanced" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("secret field")).toBeNull();
    fireEvent.click(toggle);
    expect(screen.getByText("secret field")).toBeInTheDocument();
  });
});
