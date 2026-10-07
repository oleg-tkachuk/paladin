import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({ showNotification: vi.fn() }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";

import { BucketCreateDialog } from "./BucketCreateDialog";
import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import {
  FeatureSupport,
  StorageFeature,
} from "@/gen/paladin/admin/v1/types_pb";

/** A backend as the dialog reads it, with the anonymous-read probe's result. */
function backend(
  id: string,
  anonymousRead = FeatureSupport.UNKNOWN,
  state: { enabled?: boolean; declared?: boolean } = {},
) {
  return {
    backendId: id,
    enabled: true,
    declared: true,
    ...state,
    features: [
      {
        feature: StorageFeature.ANONYMOUS_READ_POLICY,
        support: anonymousRead,
      },
    ],
  } as never;
}

function open(
  createBucket = vi.fn().mockResolvedValue({}),
  backends = [backend("primary")],
) {
  const onOpenChange = vi.fn();
  render(
    <BucketCreateDialog
      open
      onOpenChange={onOpenChange}
      backends={backends}
      createBucket={createBucket}
    />,
  );
  return { createBucket, onOpenChange };
}

const name = () => screen.getByLabelText(/Bucket name/);
const submit = () => screen.getByRole("button", { name: "Create bucket" });

describe("BucketCreateDialog", () => {
  beforeEach(() => {
    h.showNotification.mockReset();
  });

  // The API refuses a name S3 refuses; the form says so before submit.
  it("explains a name S3 refuses and holds Create", () => {
    open();
    fireEvent.change(name(), { target: { value: "bad_name" } });
    expect(name()).toHaveAttribute("aria-invalid", "true");
    expect(name()).toHaveAccessibleDescription(
      /letters, digits, dots and hyphens/,
    );
    expect(submit()).toBeDisabled();
    expect(
      screen.getByText("Fix the bucket name to continue."),
    ).toBeInTheDocument();
  });

  it("creates on the first backend with what was entered", async () => {
    const { createBucket, onOpenChange } = open();
    fireEvent.change(name(), { target: { value: "logs-2026" } });
    fireEvent.change(screen.getByLabelText("Region"), {
      target: { value: "eu-central-1" },
    });
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() =>
      expect(createBucket).toHaveBeenCalledWith(
        "primary",
        "logs-2026",
        "",
        "eu-central-1",
        true,
        null,
      ),
    );
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it("keeps a failed create open, with the reason", async () => {
    open(vi.fn().mockRejectedValue(new Error("BucketAlreadyExists")));
    fireEvent.change(name(), { target: { value: "taken" } });
    fireEvent.submit(submit().closest("form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "BucketAlreadyExists",
    );
  });

  it("says a backend is missing when none is registered", () => {
    open(undefined, []);
    expect(
      screen.getByText("Register a storage backend first."),
    ).toBeInTheDocument();
  });

  // A failed read and an empty one both leave `backends` empty; only one of
  // them means "register a backend". Shown that advice, an operator goes off
  // to register a backend that already exists.
  it("says the backend list failed rather than that none is registered", async () => {
    const retry = vi.fn();
    render(
      <BucketCreateDialog
        open
        onOpenChange={vi.fn()}
        backends={[]}
        backendsFailed={{ reason: "unavailable: upstream", retry }}
        createBucket={vi.fn()}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      /Backends could not be loaded/,
    );
    expect(
      screen.getByText("Backends could not be loaded."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Register a storage backend first."),
    ).not.toBeInTheDocument();
    expect(submit()).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();
  });

  // ADR-0027: public read only where the backend's probe found anonymous
  // reads enforced; the form says why otherwise and links to the probe.
  it("offers public read only on a backend that enforces anonymous reads", () => {
    open(undefined, [backend("primary")]);
    expect(screen.getByRole("switch", { name: "Public read" })).toBeDisabled();
    expect(
      screen.getByRole("link", { name: "Probe its features" }),
    ).toHaveAttribute("href", "/storage-backends/primary");
  });

  it("creates a public bucket with its content types and CDN address", async () => {
    const { createBucket } = open(undefined, [
      backend("primary", FeatureSupport.SUPPORTED),
    ]);
    fireEvent.change(name(), { target: { value: "photos" } });
    fireEvent.click(screen.getByRole("switch", { name: "Public read" }));
    // A public bucket must say what it serves before it can be created.
    expect(submit()).toBeDisabled();
    const types = screen.getByLabelText(/Allowed content types/);
    fireEvent.change(types, { target: { value: "image/webp" } });
    fireEvent.keyDown(types, { key: "Enter" });
    fireEvent.change(screen.getByLabelText("CDN base URL"), {
      target: { value: "https://cdn.example.com" },
    });
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() =>
      expect(createBucket).toHaveBeenCalledWith(
        "primary",
        "photos",
        "",
        "",
        true,
        {
          allowedContentTypes: ["image/webp"],
          baseUrl: "https://cdn.example.com",
        },
      ),
    );
  });

  it("points a refusal for a missing feature at the probe", async () => {
    const refused = new ConnectError(
      "backend primary: anonymous_read_policy is unknown",
      Code.FailedPrecondition,
      undefined,
      [
        {
          desc: ErrorInfoSchema,
          value: create(ErrorInfoSchema, {
            domain: "paladin",
            reason: "ERROR_REASON_BACKEND_FEATURE_UNSUPPORTED",
          }),
        },
      ],
    );
    open(vi.fn().mockRejectedValue(refused));
    fireEvent.change(name(), { target: { value: "photos" } });
    fireEvent.submit(submit().closest("form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Run Test connectivity on the backend's page/,
    );
  });

  // A backend registered only through the API cannot hold a bucket, and it
  // was the default because it sorted first.
  it("defaults to a backend that can hold a bucket", async () => {
    const apiOnly = backend("api-only", undefined, { declared: false });
    const { createBucket } = open(undefined, [apiOnly, backend("primary")]);
    fireEvent.change(name(), { target: { value: "logs" } });
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() =>
      expect(createBucket).toHaveBeenCalledWith(
        "primary",
        "logs",
        "",
        "",
        true,
        null,
      ),
    );
  });

  it("holds Create and says why when no backend can hold a bucket", () => {
    const disabled = backend("old", undefined, { enabled: false });
    open(undefined, [disabled]);
    fireEvent.change(name(), { target: { value: "logs" } });
    expect(
      screen.getByText("Backend old is disabled; pick another."),
    ).toBeInTheDocument();
    expect(submit()).toBeDisabled();
  });

  // The refusal stayed on screen after the field it was about was fixed.
  it("drops a refusal once the form changes", async () => {
    open(vi.fn().mockRejectedValue(new Error("BucketAlreadyExists")));
    fireEvent.change(name(), { target: { value: "taken" } });
    fireEvent.submit(submit().closest("form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "BucketAlreadyExists",
    );
    fireEvent.change(name(), { target: { value: "taken-2" } });
    expect(screen.queryByText("BucketAlreadyExists")).not.toBeInTheDocument();
  });
});
