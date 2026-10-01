import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({ showNotification: vi.fn() }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import { BucketCreateDialog } from "./BucketCreateDialog";

function open(
  createBucket = vi.fn().mockResolvedValue({}),
  backends = ["primary"],
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
});
