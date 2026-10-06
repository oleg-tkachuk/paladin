import { beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { render, screen } from "@/test/utils";
import userEvent from "@testing-library/user-event";

import {
  VersionInfoSchema,
  type VersionInfo,
} from "@/gen/paladin/iam/v1/health_service_pb";

const h = vi.hoisted(() => ({
  version: {
    status: "loading" as "ok" | "unavailable" | "loading",
    data: null as VersionInfo | null,
  },
  ui: { version: "11.9.0", commit: "a1b2c3d", buildTime: "" },
}));
vi.mock("@/context/ShellContext", () => ({
  useShell: () => ({ version: h.version }),
}));
vi.mock("@/lib/ui/build-info", async (orig) => ({
  ...(await orig<typeof import("@/lib/ui/build-info")>()),
  uiBuildInfo: () => h.ui,
}));

import { TooltipProvider } from "@/components/ui/Tooltip";
import { BuildVersion } from "./BuildVersion";

// The app mounts one TooltipProvider at the root (app/layout.tsx).
function show() {
  return render(
    <TooltipProvider>
      <BuildVersion fallback={fallback} />
    </TooltipProvider>,
  );
}

const BUILD_SECONDS = 1_791_280_000n; // 2026-10-06, an arbitrary release time

function backend(version: string, commit: string) {
  h.version.status = "ok";
  h.version.data = create(VersionInfoSchema, {
    version,
    commit,
    buildTime: { seconds: BUILD_SECONDS, nanos: 0 },
  });
}

const fallback = <span>Control Plane</span>;

describe("BuildVersion", () => {
  beforeEach(() => {
    h.version.status = "loading";
    h.version.data = null;
    h.ui = { version: "11.9.0", commit: "a1b2c3d", buildTime: "" };
  });

  it("shows the fallback until the backend's version arrives", () => {
    show();
    expect(screen.getByText("Control Plane")).toBeInTheDocument();
  });

  it("keeps the fallback when the version section is unavailable", () => {
    h.version.status = "unavailable";
    show();
    expect(screen.getByText("Control Plane")).toBeInTheDocument();
  });

  it("shows the backend release with its short commit, linking to /health", () => {
    backend("11.9.0", "a1b2c3d4e5f6");
    show();
    const link = screen.getByRole("link", {
      name: "Paladin v11.9.0 (a1b2c3d)",
    });
    expect(link).toHaveAttribute("href", "/health");
    expect(link).toHaveTextContent("v11.9.0 (a1b2c3d)");
    expect(screen.queryByText("Control Plane")).not.toBeInTheDocument();
  });

  it("lists the backend, its build time and the console in the tooltip", async () => {
    backend("11.9.0", "a1b2c3d");
    show();
    await userEvent.hover(screen.getByRole("link"));
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent("Backend");
    expect(tip).toHaveTextContent("Built");
    expect(tip).toHaveTextContent("Console");
    expect(tip).not.toHaveTextContent("different commits");
  });

  it("flags a console built from another commit", async () => {
    backend("11.9.0", "a1b2c3d");
    h.ui = { version: "11.8.0", commit: "9f8e7d6", buildTime: "" };
    show();
    const link = screen.getByRole("link", {
      name: "Paladin v11.9.0 (a1b2c3d), console built from a different commit",
    });
    await userEvent.hover(link);
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "built from different commits",
    );
  });

  it("does not flag a local console build, which has no commit", () => {
    backend("11.9.0", "a1b2c3d");
    h.ui = { version: "local", commit: "", buildTime: "" };
    show();
    expect(
      screen.getByRole("link", { name: "Paladin v11.9.0 (a1b2c3d)" }),
    ).toBeInTheDocument();
  });
});
