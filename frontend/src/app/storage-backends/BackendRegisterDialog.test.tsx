import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const h = vi.hoisted(() => ({ showNotification: vi.fn() }));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.showNotification }),
}));

import {
  BACKEND_ID_ERROR,
  BackendRegisterDialog,
} from "./BackendRegisterDialog";

function open(createBackend = vi.fn().mockResolvedValue({ backendId: "b" })) {
  render(
    <BackendRegisterDialog
      open
      onOpenChange={vi.fn()}
      createBackend={createBackend}
    />,
  );
  return createBackend;
}

const field = (label: RegExp) => screen.getByLabelText(label);
const type = (label: RegExp, value: string) =>
  fireEvent.change(field(label), { target: { value } });
const submit = () => screen.getByRole("button", { name: "Register backend" });

describe("BackendRegisterDialog", () => {
  beforeEach(() => h.showNotification.mockReset());

  it("explains a bad backend ID at the field", () => {
    open();
    type(/Backend ID/, "Bad_ID");
    expect(field(/Backend ID/)).toHaveAccessibleDescription(BACKEND_ID_ERROR);
    expect(submit()).toBeDisabled();
  });

  // The credentials reference was checked only on submit, by a toast.
  it("holds the submit until every required field is filled, and says which", () => {
    open();
    type(/Backend ID/, "minio-dev");
    expect(
      screen.getByText("Enter the internal endpoint."),
    ).toBeInTheDocument();
    type(/Internal endpoint/, "http://minio:9000");
    expect(
      screen.getByText("Enter the credentials reference."),
    ).toBeInTheDocument();
    expect(submit()).toBeDisabled();
  });

  it("registers what was entered", async () => {
    const createBackend = open();
    type(/Backend ID/, "minio-dev");
    type(/Internal endpoint/, "http://minio:9000");
    type(/Credentials reference/, "k8s://ns/minio");
    fireEvent.submit(submit().closest("form")!);
    await waitFor(() => expect(createBackend).toHaveBeenCalledTimes(1));
    expect(createBackend.mock.calls[0][0]).toMatchObject({
      backendId: "minio-dev",
      endpoint: "http://minio:9000",
      credentialsSecretRef: "k8s://ns/minio",
      forcePathStyle: true,
    });
  });
});
