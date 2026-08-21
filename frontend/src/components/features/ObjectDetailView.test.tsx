import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";

// Protective net for decomposing ObjectDetailView (pure helpers, the Tags
// card, the Specs sidebar). Mock the useObject hook + navigation +
// notifications; stub PageHeader, ObjectVersionsTab, and next/image. Covers
// the behaviors the extraction must preserve: header/filename, the Specs
// facts, the Object/Versions tabs, tag edit→save, and the trash confirm flow.
const h = vi.hoisted(() => ({
  object: null as Record<string, unknown> | null,
  downloadUrl: undefined as { url: string } | undefined,
  loading: false,
  refresh: vi.fn(),
  patchObjectMeta: vi.fn((_labels: Record<string, string>) =>
    Promise.resolve(),
  ),
  softDeleteObject: vi.fn(() => Promise.resolve()),
  restoreObject: vi.fn(() => Promise.resolve()),
  purgeObject: vi.fn(() => Promise.resolve()),
  showNotification: vi.fn(),
  push: vi.fn(),
}));

vi.mock("@/hooks/useObject", () => ({
  useObject: () => ({
    object: h.object,
    downloadUrl: h.downloadUrl,
    loading: h.loading,
    refresh: h.refresh,
    patchObjectMeta: h.patchObjectMeta,
    softDeleteObject: h.softDeleteObject,
    restoreObject: h.restoreObject,
    purgeObject: h.purgeObject,
  }),
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: h.push }),
  usePathname: () => "/tenants/t1/collections/ok1/objects/o1",
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));
vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({
    title,
    actions,
  }: {
    title: React.ReactNode;
    actions?: React.ReactNode;
  }) => (
    <div>
      <div data-testid="page-title">{title}</div>
      {actions}
    </div>
  ),
}));
vi.mock("@/components/features/ObjectVersionsTab", () => ({
  ObjectVersionsTab: () => <div data-testid="versions-tab" />,
}));
// The lock card owns its own query and auth context; this suite is about the
// detail view's own composition, so it stands in as a marker.
vi.mock("@/components/features/ObjectLockCard", () => ({
  ObjectLockCard: () => <div data-testid="object-lock-card" />,
}));
vi.mock("next/image", () => ({
  default: (props: Record<string, unknown>) => {
    // eslint-disable-next-line @next/next/no-img-element, jsx-a11y/alt-text
    return <img {...(props as Record<string, never>)} />;
  },
}));

import { ObjectDetailView } from "./ObjectDetailView";

const makeObject = () => ({
  objectId: "obj-uuid-1",
  key: "path/to/report.pdf",
  collection: "ok1",
  contentType: "application/pdf",
  sizeBytes: 2048n,
  state: ObjectState.AVAILABLE,
  tags: { env: "prod", object_tag: "invoice" },
  externalRef: "",
  presignExpiresAt: undefined,
});

beforeEach(() => {
  h.object = makeObject();
  h.downloadUrl = undefined;
  h.loading = false;
  h.refresh.mockClear();
  h.patchObjectMeta.mockClear();
  h.softDeleteObject.mockClear();
  h.restoreObject.mockClear();
  h.purgeObject.mockClear();
  h.showNotification.mockClear();
  h.push.mockClear();
});

describe("ObjectDetailView", () => {
  it("renders the filename, specs, and tags", () => {
    render(<ObjectDetailView collection="o1" />);
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    // Specs sidebar facts.
    expect(screen.getByText("Object UUID")).toBeInTheDocument();
    expect(screen.getByText("Storage Path")).toBeInTheDocument();
    expect(screen.getByText("Classification")).toBeInTheDocument();
    // The reserved object_tag is shown in Specs as Classification, not as a tag.
    expect(screen.getByText("invoice")).toBeInTheDocument();
    // A free-form tag renders as a badge.
    expect(screen.getByText("env=prod")).toBeInTheDocument();
  });

  it("renders the Object and Versions tabs", () => {
    render(<ObjectDetailView collection="o1" />);
    expect(screen.getByRole("tab", { name: "Object" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Versions" })).toBeInTheDocument();
  });

  it("edits and saves tags", async () => {
    render(<ObjectDetailView collection="o1" />);
    // The Tags card "Edit" button (not the header "Edit Metadata").
    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    // The key input upper-cases as you type, so "owner" lands as "OWNER".
    await userEvent.type(screen.getByLabelText("New tag key"), "owner");
    await userEvent.type(screen.getByLabelText("New tag value"), "team-a");
    await userEvent.click(screen.getByRole("button", { name: /Add/i }));
    await userEvent.click(screen.getByRole("button", { name: /^Save$/i }));
    await waitFor(() => expect(h.patchObjectMeta).toHaveBeenCalled());
    const saved = h.patchObjectMeta.mock.calls.at(-1)?.[0];
    expect(saved).toMatchObject({ env: "prod", OWNER: "team-a" });
  });

  it("soft-deletes through the trash confirm dialog", async () => {
    render(<ObjectDetailView collection="o1" />);
    await userEvent.click(
      screen.getByRole("button", { name: "Object actions" }),
    );
    await userEvent.click(screen.getByText("Move to Trash"));
    // ConfirmModal action button.
    await userEvent.click(
      screen.getByRole("button", { name: "Move to Trash" }),
    );
    await waitFor(() => expect(h.softDeleteObject).toHaveBeenCalled());
  });
});
