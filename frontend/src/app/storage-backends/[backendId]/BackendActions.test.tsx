import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";
import userEvent from "@testing-library/user-event";

// BackendActions drives BackendService through useBackends → backendClient.
// Mock the client (the four action RPCs), the router (delete redirects), and
// notifications.
const h = vi.hoisted(() => ({
  test: vi.fn(),
  update: vi.fn(),
  rotate: vi.fn(),
  del: vi.fn(),
  push: vi.fn(),
  notify: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  backendClient: {
    listBackends: vi.fn(),
    testBackend: h.test,
    updateBackend: h.update,
    rotateCredentials: h.rotate,
    deleteBackend: h.del,
  },
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: h.push }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import { BackendActions } from "./BackendActions";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";

const backend = {
  $typeName: "paladin.admin.v1.StorageBackend",
  name: "storageBackends/b1",
  backendId: "b1",
  displayName: "Primary",
  endpoint: "http://minio:9000",
  publicEndpoint: "https://s3.example.com",
  region: "us-east-1",
  forcePathStyle: true,
  credentialsSecretRef: "vault://kv/paladin/b1",
  resourceVersion: "7",
} as unknown as StorageBackend;

describe("BackendActions", () => {
  beforeEach(() => {
    Object.values(h).forEach((fn) => fn.mockReset());
    h.update.mockResolvedValue(backend);
    h.rotate.mockResolvedValue(backend);
    h.del.mockResolvedValue({});
  });

  it("probes connectivity via TestBackend and renders the result badge", async () => {
    h.test.mockResolvedValue({
      reachable: true,
      errorMessage: "",
      latencyMs: 12,
    });
    render(<BackendActions backend={backend} />);

    await userEvent.click(
      screen.getByRole("button", { name: /test connectivity/i }),
    );

    await waitFor(() =>
      expect(h.test).toHaveBeenCalledWith({ name: "storageBackends/b1" }),
    );
    expect(await screen.findByText(/reachable · 12ms/i)).toBeInTheDocument();
  });

  it("shows an unreachable badge when the probe fails to connect", async () => {
    h.test.mockResolvedValue({
      reachable: false,
      errorMessage: "dial tcp: refused",
      latencyMs: 0,
    });
    render(<BackendActions backend={backend} />);

    await userEvent.click(
      screen.getByRole("button", { name: /test connectivity/i }),
    );

    expect(
      await screen.findByText(/unreachable: dial tcp: refused/i),
    ).toBeInTheDocument();
  });

  it("edits metadata via UpdateBackend with the full field mask and OCC version", async () => {
    render(<BackendActions backend={backend} />);
    await userEvent.click(screen.getByRole("button", { name: /^edit$/i }));

    const name = await screen.findByLabelText(/display name/i);
    await userEvent.clear(name);
    await userEvent.type(name, "Primary EU");
    await userEvent.click(
      screen.getByRole("button", { name: /save changes/i }),
    );

    await waitFor(() => expect(h.update).toHaveBeenCalledTimes(1));
    const arg = h.update.mock.calls[0][0];
    expect(arg.name).toBe("storageBackends/b1");
    expect(arg.resourceVersion).toBe("7");
    expect(arg.updateMask.paths).toEqual([
      "display_name",
      "endpoint",
      "public_endpoint",
      "region",
      "force_path_style",
    ]);
    expect(arg.backend.displayName).toBe("Primary EU");
  });

  it("rotates credentials via RotateCredentials with the new ref + grace", async () => {
    render(<BackendActions backend={backend} />);
    await userEvent.click(
      screen.getByRole("button", { name: /rotate credentials/i }),
    );

    await userEvent.type(
      await screen.findByLabelText(/new secret ref/i),
      "vault://kv/paladin/b1-next",
    );
    await userEvent.type(screen.getByLabelText(/grace period/i), "30m");
    // The dialog "Rotate" submit button (distinct from the opener above).
    await userEvent.click(screen.getByRole("button", { name: /^rotate$/i }));

    await waitFor(() => expect(h.rotate).toHaveBeenCalledTimes(1));
    expect(h.rotate).toHaveBeenCalledWith({
      name: "storageBackends/b1",
      newSecretRef: "vault://kv/paladin/b1-next",
      gracePeriod: "30m",
    });
  });

  it("requires typing the id before Delete, then deletes and redirects", async () => {
    render(<BackendActions backend={backend} />);
    await userEvent.click(screen.getByRole("button", { name: /^delete$/i }));

    const submit = await screen.findByRole("button", {
      name: /delete backend/i,
    });
    // Guard: disabled until the confirm text matches the id.
    expect(submit).toBeDisabled();

    await userEvent.type(screen.getByLabelText(/confirm backend id/i), "b1");
    expect(submit).toBeEnabled();
    await userEvent.click(submit);

    await waitFor(() =>
      expect(h.del).toHaveBeenCalledWith({
        name: "storageBackends/b1",
        resourceVersion: "7",
        force: false,
      }),
    );
    expect(h.push).toHaveBeenCalledWith("/storage-backends");
  });
});
