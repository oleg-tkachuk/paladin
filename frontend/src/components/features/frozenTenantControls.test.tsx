import React from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import {
  TenantProvider,
  TRASHED_TENANT_BLOCK,
} from "@/app/tenants/[id]/tenant-context";

vi.mock("next/navigation", () => ({
  usePathname: () => "/tenants/acme/collections/docs/objects",
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/hooks/useObjectLock", () => ({
  useObjectLock: () => ({
    lock: { mode: "GOVERNANCE", legalHold: false },
    isLoading: false,
    error: null,
    setRetention: vi.fn(),
    setLegalHold: vi.fn(),
    refresh: vi.fn(),
  }),
}));

import { ObjectDetailActions } from "./ObjectDetailActions";
import { ObjectTagsCard } from "./ObjectTagsCard";
import { VersionRow } from "./VersionRow";
import { ObjectLockCard } from "./ObjectLockCard";
import { BulkActionsToolbar } from "./objects/BulkActionsToolbar";
import { ObjectTableRow } from "./objects/ObjectTableRow";

// A tenant in the trash takes no changes until it is restored; its objects
// still read and download. These controls sit on the tenant's object pages,
// which the first pass of the freeze UI did not reach.
function inTenant(trashed: boolean, ui: React.ReactElement) {
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

const noop = () => {};

function expectHeld(el: HTMLElement) {
  expect(el).toBeDisabled();
  expect(el).toHaveAttribute("title", TRASHED_TENANT_BLOCK);
}

describe("object controls on a trashed tenant", () => {
  it("hold the detail page's edits and delete menu, keep downloads", () => {
    inTenant(
      true,
      <ObjectDetailActions
        state={ObjectState.AVAILABLE}
        onEdit={noop}
        onShare={noop}
        onDownload={noop}
        onAction={noop}
      />,
    );
    expectHeld(screen.getByRole("button", { name: /Edit Metadata/ }));
    expectHeld(screen.getByRole("button", { name: "Object actions" }));
    expect(screen.getByRole("button", { name: /Download/ })).toBeEnabled();
    expect(screen.getByRole("button", { name: /Share/ })).toBeEnabled();
  });

  it("leave the detail page's edits open on a live tenant", () => {
    inTenant(
      false,
      <ObjectDetailActions
        state={ObjectState.AVAILABLE}
        onEdit={noop}
        onShare={noop}
        onDownload={noop}
        onAction={noop}
      />,
    );
    expect(screen.getByRole("button", { name: /Edit Metadata/ })).toBeEnabled();
  });

  it("hold the tags editor", () => {
    inTenant(
      true,
      <ObjectTagsCard
        tags={{}}
        isEditing={false}
        onEditToggle={noop}
        onSave={() => Promise.resolve()}
      />,
    );
    expectHeld(screen.getByRole("button", { name: "Edit" }));
  });

  it("hold a version restore, keep viewing it", () => {
    inTenant(
      true,
      <VersionRow
        version={
          {
            versionId: "v1",
            isCurrent: false,
            isDeleteMarker: false,
            sizeBytes: 1n,
          } as unknown as ObjectVersion
        }
        onView={noop}
        onRestore={noop}
      />,
    );
    expectHeld(screen.getByRole("button", { name: /Restore/ }));
    expect(screen.getByRole("button", { name: /View/ })).toBeEnabled();
  });

  it("hold the legal hold switch", () => {
    inTenant(true, <ObjectLockCard objectName="tenants/acme/o" />);
    expectHeld(screen.getByRole("switch"));
  });

  it("hold the bulk actions", () => {
    inTenant(
      true,
      <BulkActionsToolbar
        selectedCount={2}
        onEditLabels={noop}
        onBulkDelete={noop}
        onClearSelection={noop}
        isProcessing={false}
      />,
    );
    expectHeld(screen.getByRole("button", { name: /Delete/ }));
    expectHeld(screen.getByRole("button", { name: /Sync Labels/ }));
  });

  it("drop a row's changes from its menu and say why", async () => {
    const obj = {
      objectId: "o1",
      key: "report.pdf",
      collection: "docs",
      name: "tenants/acme/collections/docs/objects/report.pdf",
      contentType: "application/pdf",
      sizeBytes: 1n,
      state: ObjectState.AVAILABLE,
      tags: {},
    } as unknown as Object$;
    inTenant(
      true,
      <table>
        <tbody>
          <ObjectTableRow
            obj={obj}
            isSelected={false}
            isInspected={false}
            isEditingLabels={false}
            editLabelsValue=""
            useRelativeTime={false}
            visibleColumns={new Set(["actions", "tags"])}
            onToggleSelect={noop}
            onInspect={noop}
            onStartInlineEdit={noop}
            onSaveInlineLabels={noop}
            onCancelInlineEdit={noop}
            onEditLabelsValueChange={noop}
            onCopyToClipboard={noop}
            onSoftDelete={noop}
            onHardDelete={noop}
            onCopy={noop}
            onMove={noop}
            onGenerateDownloadUrl={() => Promise.resolve(undefined)}
          />
        </tbody>
      </table>,
    );
    await userEvent.click(screen.getByRole("button", { name: /actions/i }));
    expect(screen.queryByText("Hard Delete")).not.toBeInTheDocument();
    expect(screen.queryByText("Soft Delete")).not.toBeInTheDocument();
    expect(screen.queryByText("Move Object")).not.toBeInTheDocument();
    expect(screen.queryByText("Apply Labels")).not.toBeInTheDocument();
    expect(screen.getByText(TRASHED_TENANT_BLOCK)).toBeInTheDocument();
    expect(screen.getByText("Copy Download Link")).toBeInTheDocument();
  });
});
