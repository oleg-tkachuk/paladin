import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const baseParams =
  "client_id=desktop-agent&scope=paladin.read paladin.write&redirect_uri=desktop-agent%3A%2F%2Fcb&state=st&code_challenge=abc&code_challenge_method=S256";

let searchString = baseParams;

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(searchString),
}));

import ConsentPage from "./page";

beforeEach(() => {
  searchString = baseParams;
});

describe("OAuth ConsentPage", () => {
  it("shows the requesting client and the requested scopes", () => {
    render(<ConsentPage />);
    expect(screen.getByText("desktop-agent")).toBeInTheDocument();
    expect(screen.getByText("paladin.read")).toBeInTheDocument();
    expect(screen.getByText("paladin.write")).toBeInTheDocument();
  });

  it("posts the OAuth params + decision back to /oauth/authorize", () => {
    const { container } = render(<ConsentPage />);
    const form = container.querySelector("form")!;
    expect(form.getAttribute("method")).toBe("post");
    expect(form.getAttribute("action")).toBe("/oauth/authorize");

    // OAuth request params are round-tripped as hidden fields.
    const hidden = (name: string) =>
      form.querySelector<HTMLInputElement>(`input[name="${name}"]`)?.value;
    expect(hidden("client_id")).toBe("desktop-agent");
    expect(hidden("redirect_uri")).toBe("desktop-agent://cb");
    expect(hidden("code_challenge")).toBe("abc");
    expect(hidden("code_challenge_method")).toBe("S256");
    expect(hidden("state")).toBe("st");

    // Allow + Deny submit the action discriminator.
    const actions = Array.from(
      form.querySelectorAll<HTMLButtonElement>('button[name="action"]'),
    ).map((b) => b.value);
    expect(actions).toContain("allow");
    expect(actions).toContain("deny");
  });

  describe("skip_consent (pre-authorized client)", () => {
    beforeEach(() => {
      searchString = `${baseParams}&skip_consent=1`;
    });

    it("renders a plain login — no scope list, no Deny", () => {
      const { container } = render(<ConsentPage />);
      expect(
        screen.getByRole("heading", { name: "Sign in" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Sign in" }),
      ).toBeInTheDocument();
      // Scope list is suppressed for a trusted client.
      expect(screen.queryByText("paladin.read")).not.toBeInTheDocument();
      const actions = Array.from(
        container.querySelectorAll<HTMLButtonElement>('button[name="action"]'),
      ).map((b) => b.value);
      expect(actions).not.toContain("deny");
    });

    it("still submits action=allow via a hidden field", () => {
      const { container } = render(<ConsentPage />);
      const form = container.querySelector("form")!;
      const action = form.querySelector<HTMLInputElement>(
        'input[name="action"]',
      );
      expect(action?.value).toBe("allow");
    });
  });
});
