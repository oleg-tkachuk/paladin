import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("next/navigation", () => ({
  useSearchParams: () =>
    new URLSearchParams(
      "client_id=claude-desktop&scope=paladin.read paladin.write&redirect_uri=claude-desktop%3A%2F%2Fcb&state=st&code_challenge=abc&code_challenge_method=S256",
    ),
}));

import ConsentPage from "./page";

describe("OAuth ConsentPage", () => {
  it("shows the requesting client and the requested scopes", () => {
    render(<ConsentPage />);
    expect(screen.getByText("claude-desktop")).toBeInTheDocument();
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
    expect(hidden("client_id")).toBe("claude-desktop");
    expect(hidden("redirect_uri")).toBe("claude-desktop://cb");
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
});
