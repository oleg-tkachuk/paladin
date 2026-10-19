import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({ simulateAuthz: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  policyClient: { simulateAuthz: h.simulateAuthz },
}));
vi.mock("@/context/ScopeContext", () => ({
  useScope: () => ({ tenantId: "t-1" }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import { TestSuite } from "./_TestSuite";

const SAVED_KEY = "paladin:policies:testSuite:t-1";

// A case saved before the kind was asked for: it simulates a signed-in user,
// which is what the console's own callers are.
const savedWithoutKind = {
  id: "tc_1",
  name: "machine deletes its object",
  principalSubject: "svc",
  principalTenantId: "t-1",
  rolesText: "",
  action: "DeleteObject",
  resourceName: "tenants/t-1/collections/c",
  expected: "any",
};

beforeEach(() => {
  h.simulateAuthz.mockReset();
  h.simulateAuthz.mockResolvedValue({
    allowed: true,
    matchedPolicies: [],
    explanation: "",
  });
  window.localStorage.clear();
});

// Without the kind a policy that reads principal.kind — the built-in permit
// letting a machine delete its own objects is one — answered "denied" for a
// call that succeeds.
describe("TestSuite principal kind", () => {
  it("simulates the kind the case names, and a user for an old case", async () => {
    window.localStorage.setItem(SAVED_KEY, JSON.stringify([savedWithoutKind]));
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TestSuite defaultResource="" />);

    await user.click(await screen.findByRole("button", { name: /Run all/ }));
    await waitFor(() =>
      expect(h.simulateAuthz).toHaveBeenLastCalledWith(
        expect.objectContaining({ principalKind: "user" }),
      ),
    );

    await user.click(screen.getByRole("combobox", { name: "Principal kind" }));
    await user.click(await screen.findByRole("option", { name: "API key" }));
    await user.click(screen.getByRole("button", { name: /Run all/ }));
    await waitFor(() =>
      expect(h.simulateAuthz).toHaveBeenLastCalledWith(
        expect.objectContaining({ principalKind: "api_key" }),
      ),
    );
  });
});
