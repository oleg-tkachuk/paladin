import { beforeEach, describe, expect, it, vi } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { render, screen, typeToConfirm, waitFor } from "@/test/utils";

const FORMER = "01a1e6cd-2efe-7a61-a78e-5ecad091c778";

const h = vi.hoisted(() => ({
  slugs: [] as unknown[],
  enforced: true,
  releaseSlug: vi.fn(),
  notify: vi.fn(),
}));

vi.mock("@/hooks/useRetiredTenantSlugs", () => ({
  useRetiredTenantSlugs: () => ({
    slugs: h.slugs,
    enforced: h.enforced,
    loading: false,
    error: null,
    releaseSlug: h.releaseSlug,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: h.notify }),
}));

import { RetiredSlugs } from "./RetiredSlugs";

function retired(slug: string) {
  return {
    name: `retiredTenantSlugs/${slug}`,
    slug,
    formerTenantId: FORMER,
    retireTime: timestampFromDate(new Date()),
  };
}

describe("RetiredSlugs", () => {
  beforeEach(() => {
    h.slugs = [];
    h.enforced = true;
    h.releaseSlug.mockReset().mockResolvedValue(undefined);
    h.notify.mockReset();
  });

  it("says when there are none", () => {
    render(<RetiredSlugs />);
    expect(screen.getByText(/No retired slugs/)).toBeInTheDocument();
  });

  it("lists each slug with the tenant that held it", () => {
    h.slugs = [retired("acme")];
    render(<RetiredSlugs />);
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(screen.getByText(FORMER)).toBeInTheDocument();
  });

  it("says when reservation is off", () => {
    h.enforced = false;
    render(<RetiredSlugs />);
    expect(screen.getByText(/Reservation is off/)).toBeInTheDocument();
  });

  it("releases only after the slug is typed", async () => {
    h.slugs = [retired("acme")];
    render(<RetiredSlugs />);
    screen.getByRole("button", { name: "Release acme" }).click();
    const confirm = await screen.findByRole("button", { name: "Release slug" });
    expect(confirm).toBeDisabled();
    expect(h.releaseSlug).not.toHaveBeenCalled();
    await typeToConfirm("acme");
    confirm.click();
    await waitFor(() => expect(h.releaseSlug).toHaveBeenCalledWith("acme"));
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ type: "success" }),
      ),
    );
  });

  it("reports a refused release", async () => {
    h.slugs = [retired("acme")];
    h.releaseSlug.mockRejectedValue(new Error("denied"));
    render(<RetiredSlugs />);
    screen.getByRole("button", { name: "Release acme" }).click();
    const confirm = await screen.findByRole("button", { name: "Release slug" });
    await typeToConfirm("acme");
    confirm.click();
    await waitFor(() =>
      expect(h.notify).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error", message: "denied" }),
      ),
    );
  });
});
