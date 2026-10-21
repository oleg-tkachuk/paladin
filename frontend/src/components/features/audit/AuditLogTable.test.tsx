import { describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen, within } from "@/test/utils";

import { AuditLogEntrySchema } from "@/gen/paladin/admin/v1/types_pb";

vi.mock("./ActorName", () => ({
  ActorName: ({ subject }: { subject: string }) => subject,
}));

import { AUDIT_ORDER } from "@/lib/auditOrder";
import { SHOWN_IN_CARD } from "@/components/ui/table";

import { AuditLogTable } from "./AuditLogTable";

const TENANT = "01a0fcfc-281e-7c59-963c-8a946fe82a47";
const RESOURCE = `tenants/${TENANT}/capabilities/01a15d9a-6faf-7269-9707-13b4a331f80c`;
const CAPABILITY = "9f6c0f15-de3c-4b2a-8c1d-7f00fed54c48";

const ENTRY = create(AuditLogEntrySchema, {
  entryId: "entry-1",
  action: "/paladin.admin.v1.CapabilityService/Revoke",
  actorSubject: "apikey:c96e0f84-b32b-405d-81af-2a091f0faea6",
  actorTenantId: TENANT,
  actorAudience: "paladin-admin",
  resourceName: RESOURCE,
  requestId: "b36bacb9-77c4-4d8e-9a0b-1c2d3e4f5a6b",
  sourceIp: "192.168.194.123",
  capabilityId: CAPABILITY,
  userAgent: "paladin-sdk-go/0.97.0 rag-api/2.1",
});

function table(onOpen = vi.fn()) {
  render(
    <AuditLogTable
      entries={[ENTRY]}
      loading={false}
      filtered={false}
      sort={{ column: null, direction: null }}
      onSort={vi.fn()}
      onOpen={onOpen}
      rowProps={() => ({})}
    />,
  );
  return onOpen;
}

describe("AuditLogTable", () => {
  it("sorts by the client that made each call", () => {
    const onSort = vi.fn();
    render(
      <AuditLogTable
        entries={[ENTRY]}
        loading={false}
        filtered={false}
        sort={{ column: null, direction: null }}
        onSort={onSort}
        onOpen={vi.fn()}
        rowProps={() => ({})}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Client/ }));
    expect(onSort).toHaveBeenCalledWith(AUDIT_ORDER.client);
  });

  it("has a column for the client that made each call", () => {
    table();
    expect(
      screen.getByRole("columnheader", { name: "Client" }),
    ).toBeInTheDocument();
    expect(screen.getByText("rag-api/2.1")).toBeInTheDocument();
    expect(screen.getByText("Go SDK 0.97.0")).toBeInTheDocument();
  });

  // A phone's card has room for the client and the resource the narrow
  // table hides, so they are on it.
  it("puts the client and the resource on a phone's card", () => {
    table();
    expect(screen.getByText("rag-api/2.1").closest("td")).toHaveClass(
      SHOWN_IN_CARD,
    );
    expect(screen.getByTitle(RESOURCE).closest("td")).toHaveClass(
      SHOWN_IN_CARD,
    );
  });

  // The tenant takes most of a resource's width and the actor already says
  // it; the full name is the tooltip.
  it("shows a resource without its tenant, the full name on hover", () => {
    table();
    const cell = screen.getByTitle(RESOURCE);
    expect(cell).toHaveTextContent(/^capabilities\/01a15d9a-/);
  });

  it("keeps long identifiers to one line, whole in the tooltip", () => {
    table();
    expect(screen.getByTitle(ENTRY.requestId)).toHaveTextContent(/^b36bacb9$/);
    expect(screen.getByText(ENTRY.sourceIp)).toBeInTheDocument();
  });

  it("links a capability to its page, without opening the row", () => {
    const onOpen = table();
    const link = screen.getByRole("link", { name: CAPABILITY.slice(0, 8) });
    expect(link).toHaveAttribute(
      "href",
      `/tenants/${TENANT}/capabilities?id=${CAPABILITY}`,
    );
    fireEvent.click(link);
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("opens an entry from its row", () => {
    const onOpen = table();
    const row = screen.getByRole("row", { name: /Revoke/ });
    fireEvent.click(within(row).getByText("paladin-admin"));
    expect(onOpen).toHaveBeenCalledWith("entry-1");
  });

  it("says why it is empty under a filter", () => {
    render(
      <AuditLogTable
        entries={[]}
        loading={false}
        filtered
        sort={{ column: null, direction: null }}
        onSort={vi.fn()}
        onOpen={vi.fn()}
        rowProps={() => ({})}
      />,
    );
    expect(screen.getByText(/audit entries/i)).toBeInTheDocument();
  });
});
