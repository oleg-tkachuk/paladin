import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({
  buckets: [] as Array<Record<string, unknown>>,
  bucketsError: null as string | null,
  showNotification: vi.fn(),
}));
vi.mock("@/hooks/useBuckets", () => ({
  useBuckets: () => ({
    buckets: h.buckets,
    error: h.bucketsError,
    fetchBuckets: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { CollectionCreateDialog } from "./CollectionCreateDialog";

function open(
  backends = ["empty", "full"],
  create = vi.fn().mockResolvedValue({}),
) {
  const onCreated = vi.fn();
  render(
    <CollectionCreateDialog
      open
      onOpenChange={vi.fn()}
      backends={backends}
      bucketsHref="/buckets"
      createCollection={create}
      onCreated={onCreated}
    />,
  );
  return { create, onCreated };
}

const submit = () => screen.getByRole("button", { name: "Create Collection" });

describe("CollectionCreateDialog", () => {
  beforeEach(() => {
    h.buckets = [{ backendId: "full", bucketId: "b1", displayName: "" }];
    h.bucketsError = null;
    h.showNotification.mockReset();
  });

  // An empty backend first in the list used to be the default, which left a
  // dialog that could not be submitted.
  it("defaults to a backend that has a bucket, and picks it", async () => {
    const { create, onCreated } = open();
    fireEvent.change(screen.getByLabelText(/Path/), {
      target: { value: "Invoices/2026" },
    });
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith(
        "invoices/2026",
        "",
        "full",
        "b1",
        "",
        null,
      ),
    );
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
  });

  it("says what is missing while the submit is held", () => {
    open();
    expect(screen.getByText("Enter a path to continue.")).toBeInTheDocument();
    expect(submit()).toBeDisabled();
  });

  it("points to the buckets page when the backend has none", () => {
    h.buckets = [];
    open(["empty"]);
    expect(screen.getByText(/has no bucket/)).toBeInTheDocument();
    expect(screen.getByText("Pick a bucket to continue.")).toBeInTheDocument();
  });

  it("keeps a failed create open, with the reason", async () => {
    open(undefined, vi.fn().mockRejectedValue(new Error("already exists")));
    fireEvent.change(screen.getByLabelText(/Path/), { target: { value: "x" } });
    fireEvent.submit(submit().closest("form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "already exists",
    );
  });

  it("says the bucket list failed rather than that the backend has none", () => {
    h.buckets = [];
    h.bucketsError = "unavailable: upstream";
    open();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Buckets could not be loaded/,
    );
    expect(
      screen.getByText("Buckets could not be loaded."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/has no bucket/)).not.toBeInTheDocument();
    expect(submit()).toBeDisabled();
  });

  it("says the backend list failed rather than that none can take a Collection", () => {
    render(
      <CollectionCreateDialog
        open
        onOpenChange={vi.fn()}
        backends={[]}
        backendsFailed={{ reason: "unavailable: upstream", retry: vi.fn() }}
        bucketsHref="/buckets"
        createCollection={vi.fn()}
      />,
    );
    expect(screen.getAllByRole("alert")[0]).toHaveTextContent(
      /Backends could not be loaded/,
    );
    expect(
      screen.getByText("Backends could not be loaded."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("No backend can take a Collection."),
    ).not.toBeInTheDocument();
  });

  // ADR-0027: the bucket decides; a private one is the default, and choosing
  // a public one is said out loud and carries its Cache-Control.
  it("defaults to a private bucket when the backend has both", () => {
    h.buckets = [
      { backendId: "full", bucketId: "pub", displayName: "", publicRead: true },
      {
        backendId: "full",
        bucketId: "priv",
        displayName: "",
        publicRead: false,
      },
    ];
    open(["full"]);
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^Bucket/)).toHaveTextContent("priv");
  });

  it("creates a public Collection on a public bucket, with its Cache-Control", async () => {
    h.buckets = [
      { backendId: "full", bucketId: "pub", displayName: "", publicRead: true },
    ];
    const { create } = open(["full"]);
    expect(screen.getByRole("note")).toHaveTextContent(
      /anyone with an object.s URL will read it/,
    );
    fireEvent.change(screen.getByLabelText(/Path/), {
      target: { value: "photos" },
    });
    fireEvent.change(screen.getByLabelText("Cache-Control"), {
      target: { value: "public, max-age=600" },
    });
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith("photos", "", "full", "pub", "", {
        cacheControl: "public, max-age=600",
      }),
    );
  });
});
