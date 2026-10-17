import { describe, expect, it } from "vitest";
import { render, screen, within } from "@/test/utils";

import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import { VersionDetailsDialog } from "./VersionDetailsDialog";

const VERSION = {
  versionId: "v-0001",
  name: "tenants/t/collections/c/objects/o",
  objectId: "6f1c0000-0000-0000-0000-000000000001",
  isCurrent: true,
  isDeleteMarker: false,
  sizeBytes: 2048n,
  contentType: "application/json",
  etag: "etag-1",
  storagePath: "buckets/b/key-1",
  metadata: { owner: "ops" },
  tags: {},
} as unknown as ObjectVersion;

describe("VersionDetailsDialog", () => {
  it("shows every field of the version, wrapped", () => {
    render(
      <VersionDetailsDialog version={VERSION} open onOpenChange={() => {}} />,
    );
    const dialog = screen.getByRole("dialog", { name: "Version details" });
    expect(dialog).toHaveAccessibleDescription("v-0001");
    expect(within(dialog).getByText("current")).toBeInTheDocument();
    expect(within(dialog).getByText(VERSION.name)).toHaveClass("break-all");
    expect(within(dialog).getByText("buckets/b/key-1")).toBeInTheDocument();
    expect(within(dialog).getByText("owner = ops")).toBeInTheDocument();
  });

  it("marks an absent map with a dash and no current badge on an old version", () => {
    render(
      <VersionDetailsDialog
        version={
          { ...VERSION, isCurrent: false, metadata: {} } as ObjectVersion
        }
        open
        onOpenChange={() => {}}
      />,
    );
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).queryByText("current")).toBeNull();
    for (const label of ["Metadata", "Tags"]) {
      expect(
        within(dialog).getByText(label).nextElementSibling,
      ).toHaveTextContent(/^—$/);
    }
  });
});
