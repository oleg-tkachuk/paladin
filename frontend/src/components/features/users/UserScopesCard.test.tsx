import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ScopeType } from "@/gen/paladin/common/v1/scope_pb";

// The console listed users but could not change their scopes: GrantScopes and
// RevokeScopes had no caller, so narrowing a user meant the API.
const h = vi.hoisted(() => ({
  grantScopes: vi.fn(),
  revokeScopes: vi.fn(),
  onChanged: vi.fn(),
}));
vi.mock("@/hooks/useUserAdmin", () => ({
  useUserAdmin: () => ({
    busy: false,
    grantScopes: h.grantScopes,
    revokeScopes: h.revokeScopes,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import { UserScopesCard } from "./UserScopesCard";

const NAME = "tenants/t-1/users/u-1";

function renderCard(scopes = [{ type: ScopeType.BUCKET, value: "media" }]) {
  render(
    <UserScopesCard user={{ name: NAME, scopes }} onChanged={h.onChanged} />,
  );
}

const value = () => screen.getByLabelText("Scope value");
const grant = () => screen.getByRole("button", { name: "Grant" });

beforeEach(() => {
  h.grantScopes.mockReset().mockResolvedValue({ ok: true });
  h.revokeScopes.mockReset().mockResolvedValue({ ok: true });
  h.onChanged.mockReset();
});

describe("UserScopesCard", () => {
  it("lists the scopes as a token carries them", () => {
    renderCard();
    expect(screen.getByText("bucket:media")).toBeInTheDocument();
  });

  it("says what no scopes means", () => {
    renderCard([]);
    expect(screen.getByText(/the roles reach everything/)).toBeInTheDocument();
  });

  // Revoking the last scope leaves the user unscoped — wider, not narrower.
  it("asks before revoking, and says when the user ends up unscoped", () => {
    renderCard();
    fireEvent.click(
      screen.getByRole("button", { name: "Revoke bucket:media" }),
    );
    expect(h.revokeScopes).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      /last scope.*reach everything/,
    );
  });

  it("names only the scope lost when others remain", () => {
    renderCard([
      { type: ScopeType.BUCKET, value: "media" },
      { type: ScopeType.BUCKET, value: "archive" },
    ]);
    fireEvent.click(
      screen.getByRole("button", { name: "Revoke bucket:media" }),
    );
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      /other scopes still limit/,
    );
  });

  it("revokes one scope once confirmed", async () => {
    renderCard();
    fireEvent.click(
      screen.getByRole("button", { name: "Revoke bucket:media" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() =>
      expect(h.revokeScopes).toHaveBeenCalledWith(NAME, [
        { type: ScopeType.BUCKET, value: "media" },
      ]),
    );
    expect(h.onChanged).toHaveBeenCalled();
  });

  it("grants the typed scope, trimmed", async () => {
    renderCard();
    fireEvent.change(value(), { target: { value: " archive " } });
    fireEvent.click(grant());
    await waitFor(() =>
      expect(h.grantScopes).toHaveBeenCalledWith(NAME, [
        { type: ScopeType.BUCKET, value: "archive" },
      ]),
    );
    expect(value()).toHaveValue("");
  });

  it("holds Grant with no value, or one already held", () => {
    renderCard();
    expect(grant()).toBeDisabled();
    fireEvent.change(value(), { target: { value: "media" } });
    expect(grant()).toBeDisabled();
    expect(grant()).toHaveAttribute(
      "title",
      "The user already holds this scope.",
    );
  });

  it("keeps the value when the grant is refused", async () => {
    h.grantScopes.mockResolvedValue({ ok: false, error: "denied" });
    renderCard();
    fireEvent.change(value(), { target: { value: "archive" } });
    fireEvent.click(grant());
    await waitFor(() => expect(h.grantScopes).toHaveBeenCalled());
    expect(value()).toHaveValue("archive");
    expect(h.onChanged).not.toHaveBeenCalled();
  });
});
