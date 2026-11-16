import React from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import { TaintSignal } from "@/gen/paladin/data/v1/types_pb";
import {
  TenantProvider,
  TRASHED_TENANT_BLOCK,
} from "@/app/tenants/[id]/tenant-context";

import { ObjectTaintCard } from "./ObjectTaintCard";
import { TaintBadge } from "./objects/TaintBadge";

function inTenant(ui: React.ReactElement, trashed = false) {
  return render(
    <TenantProvider
      value={{
        tenantId: "acme-uuid",
        slug: "acme",
        displayName: "Acme",
        storageLayout: "shared",
        trashed,
      }}
    >
      {ui}
    </TenantProvider>,
  );
}

const box = (name: string) =>
  screen.getByRole("checkbox", { name: new RegExp(name) });
const save = () => screen.getByRole("button", { name: "Save taint" });

describe("ObjectTaintCard", () => {
  it("shows which signals the object carries and why an agent is refused", () => {
    inTenant(<ObjectTaintCard signals={[TaintSignal.PII]} onSave={vi.fn()} />);
    expect(box("Personal data")).toBeChecked();
    expect(box("Secrets")).not.toBeChecked();
    expect(
      screen.getByText(/refused this object: Personal data/),
    ).toBeInTheDocument();
    expect(save()).toBeDisabled();
  });

  it("adds a signal at once: it only narrows who reads", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    inTenant(<ObjectTaintCard signals={[TaintSignal.PII]} onSave={onSave} />);
    await userEvent.click(box("Secrets"));
    await userEvent.click(save());
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(onSave).toHaveBeenCalledWith([TaintSignal.PII, TaintSignal.SECRETS]);
  });

  it("asks before clearing a signal, and names it", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    inTenant(
      <ObjectTaintCard
        signals={[TaintSignal.PII, TaintSignal.SECRETS]}
        onSave={onSave}
      />,
    );
    await userEvent.click(box("Personal data"));
    await userEvent.click(save());
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      /Clearing Personal data re-opens this object/,
    );
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith([TaintSignal.SECRETS]),
    );
  });

  it("keeps the dialog open with the reason when the server refuses", async () => {
    const onSave = vi.fn().mockRejectedValue(new Error("permission denied"));
    inTenant(<ObjectTaintCard signals={[TaintSignal.PII]} onSave={onSave} />);
    await userEvent.click(box("Personal data"));
    await userEvent.click(save());
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "permission denied",
    );
  });

  it("follows the object when it reloads with other signals", () => {
    const { rerender } = inTenant(
      <ObjectTaintCard signals={[TaintSignal.PII]} onSave={vi.fn()} />,
    );
    rerender(
      <TenantProvider
        value={{
          tenantId: "acme-uuid",
          slug: "acme",
          displayName: "Acme",
          storageLayout: "shared",
          trashed: false,
        }}
      >
        <ObjectTaintCard signals={[TaintSignal.SECRETS]} onSave={vi.fn()} />
      </TenantProvider>,
    );
    expect(box("Personal data")).not.toBeChecked();
    expect(box("Secrets")).toBeChecked();
    expect(save()).toBeDisabled();
  });

  it("takes no change while the tenant is in the trash", () => {
    inTenant(
      <ObjectTaintCard signals={[TaintSignal.PII]} onSave={vi.fn()} />,
      true,
    );
    expect(box("Personal data")).toBeDisabled();
    expect(save()).toBeDisabled();
    expect(save()).toHaveAttribute("title", TRASHED_TENANT_BLOCK);
  });
});

describe("TaintBadge", () => {
  it("says nothing for an object that is not tainted", () => {
    const { container } = render(<TaintBadge signals={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("names the signals, and spells each out in its title", () => {
    render(
      <TaintBadge
        signals={[TaintSignal.PROMPT_INJECTION, TaintSignal.SECRETS]}
      />,
    );
    const badge = screen.getByTestId("taint-badge");
    expect(badge).toHaveTextContent("Tainted · Prompt injection, Secrets");
    expect(badge.getAttribute("title")).toMatch(
      /Secrets: A credential, key or token\./,
    );
  });
});
