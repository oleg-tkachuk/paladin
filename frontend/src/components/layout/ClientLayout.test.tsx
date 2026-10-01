import { describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";

vi.mock("next/navigation", () => ({ usePathname: () => "/login" }));
vi.mock("@/hooks/useGlobalShortcuts", () => ({ useGlobalShortcuts: () => {} }));

import { ClientLayout } from "./ClientLayout";

describe("ClientLayout", () => {
  it("names the document after the route", async () => {
    render(
      <ClientLayout>
        <p>page</p>
      </ClientLayout>,
    );
    await waitFor(() => expect(document.title).toBe("Sign in · Paladin"));
  });
});
