import { describe, expect, it, vi } from "vitest";
import { render } from "@/test/utils";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import { TenantProvider } from "@/app/tenants/[id]/tenant-context";
import { TooltipProvider } from "@/components/ui/Tooltip";

vi.mock("next/navigation", () => ({
  usePathname: () => "/tenants/acme/collections/docs/objects",
  useRouter: () => ({ push: vi.fn() }),
}));

import { ObjectsTable, type ObjectRowCallbacks } from "./ObjectsTable";

const noop = () => {};
const rowProps: ObjectRowCallbacks = {
  onToggleSelect: noop,
  onInspect: noop,
  onStartInlineEdit: noop,
  onSaveInlineLabels: noop,
  onCancelInlineEdit: noop,
  onEditLabelsValueChange: noop,
  onCopyToClipboard: noop,
  onSoftDelete: noop,
  onHardDelete: noop,
  onCopy: noop,
  onMove: noop,
  onGenerateDownloadUrl: () => Promise.resolve(undefined),
};
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
// Every column the header can show.
const ALL_COLUMNS = [
  "key",
  "object_tag",
  "mime",
  "size",
  "status",
  "created",
  "actions",
];

// The visibility classes of a cell, e.g. ["hidden", "@4xl:table-cell"].
const visibility = (el: Element) =>
  Array.from(el.classList).filter(
    (c) => c === "hidden" || c.endsWith(":table-cell"),
  );

// The MIME header hid on narrow tables while its row cell did not, so every
// later column sat under the wrong heading. Header and row now share
// OBJECT_COLUMN_CLASS; this holds them to it, column by column.
describe("ObjectsTable", () => {
  it("hides each column in the header and the row at the same width", () => {
    const { container } = render(
      <TenantProvider
        value={{
          tenantId: "acme-uuid",
          slug: "acme",
          displayName: "Acme",
          storageLayout: "shared",
          trashed: false,
        }}
      >
        <TooltipProvider>
          <ObjectsTable
            objects={[obj]}
            loading={false}
            error={null}
            nextCursor={undefined}
            onLoadMore={noop}
            onRefresh={noop}
            selectedIds={new Set()}
            allChecked={false}
            someChecked={false}
            onToggleSelectAll={noop}
            sort={{ column: "key", direction: "asc" }}
            onSort={noop}
            visibleColumns={new Set(ALL_COLUMNS)}
            useRelativeTime={false}
            onToggleRelativeTime={noop}
            selectedInspectorKey={null}
            editingLabelsId={null}
            editLabelsValue=""
            search=""
            status={undefined}
            rowProps={rowProps}
          />
        </TooltipProvider>
      </TenantProvider>,
    );
    const heads = Array.from(container.querySelectorAll("thead th"));
    const cells = Array.from(container.querySelector("tbody tr")!.children);
    expect(cells).toHaveLength(heads.length);
    heads.forEach((th, i) => {
      expect(visibility(cells[i]), th.textContent ?? "").toEqual(
        visibility(th),
      );
    });
  });
});
