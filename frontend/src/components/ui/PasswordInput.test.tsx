import { describe, expect, it } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen } from "@/test/utils";

import { PasswordInput } from "./PasswordInput";

describe("PasswordInput", () => {
  it("is hidden until asked, and hides again", async () => {
    render(<PasswordInput aria-label="Secret" defaultValue="s3cret" />);
    const input = screen.getByLabelText("Secret");
    expect(input).toHaveAttribute("type", "password");
    await userEvent.click(
      screen.getByRole("button", { name: "Show password" }),
    );
    expect(input).toHaveAttribute("type", "text");
    await userEvent.click(
      screen.getByRole("button", { name: "Hide password" }),
    );
    expect(input).toHaveAttribute("type", "password");
  });

  it("does not submit a form when toggled", async () => {
    let submitted = false;
    render(
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submitted = true;
        }}
      >
        <PasswordInput aria-label="Secret" />
      </form>,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Show password" }),
    );
    expect(submitted).toBe(false);
  });
});
