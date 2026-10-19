import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen } from "@/test/utils";
import { THEME_EMBER, THEME_OPTIONS } from "@/lib/theme";

import { ThemePicker } from "./ThemePicker";

describe("ThemePicker", () => {
  it("offers every theme, with the saved one checked", () => {
    render(
      <>
        <span id="t">Theme</span>
        <ThemePicker value="dark" onChange={vi.fn()} labelledBy="t" />
      </>,
    );
    const group = screen.getByRole("radiogroup", { name: "Theme" });
    const radios = screen.getAllByRole("radio");
    expect(group).toBeInTheDocument();
    expect(radios.map((r) => r.textContent)).toEqual(
      THEME_OPTIONS.map((o) => o.label),
    );
    expect(screen.getByRole("radio", { name: "Dark" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });

  it("draws each swatch in its own palette", () => {
    render(<ThemePicker value="dark" onChange={vi.fn()} labelledBy="t" />);
    const ember = screen.getByRole("radio", { name: "Ember" });
    expect(ember.querySelector(`.${THEME_EMBER}`)).not.toBeNull();
  });

  it("reports the theme picked", async () => {
    const onChange = vi.fn();
    render(<ThemePicker value="dark" onChange={onChange} labelledBy="t" />);
    await userEvent.click(screen.getByRole("radio", { name: "Ember" }));
    expect(onChange).toHaveBeenCalledWith(THEME_EMBER);
  });
});
