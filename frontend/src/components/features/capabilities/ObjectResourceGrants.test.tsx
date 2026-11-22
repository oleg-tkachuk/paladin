import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@/test/utils";

const h = vi.hoisted(() => ({ listCollections: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: { listCollections: h.listCollections },
}));

import {
  DUPLICATE_GRANT,
  type ObjectGrantDraft,
} from "@/lib/capability/objectResource";
import {
  OBJECT_GRANTS_UNRESTRICTED,
  ObjectResourceGrants,
} from "./ObjectResourceGrants";

const TENANT = "0190a3b2-7c4d-7e8f-9a0b-1c2d3e4f5a6b";
const NESTED = "invoices/2026/q1";
const COLLECTIONS = [
  { tenantId: TENANT, collection: "docs", displayName: "" },
  { tenantId: TENANT, collection: NESTED, displayName: "Q1 invoices" },
];

/** The picker over its own state, as the dialog holds it. */
function Harness({ onChange }: { onChange?: (g: ObjectGrantDraft[]) => void }) {
  const [grants, setGrants] = useState<ObjectGrantDraft[]>([]);
  return (
    <ObjectResourceGrants
      tenantId={TENANT}
      grants={grants}
      onChange={(next) => {
        setGrants(next);
        onChange?.(next);
      }}
    />
  );
}

const row = (n: number) =>
  screen.getByRole("listitem", { name: `Resource ${n}` });

async function pickCollection(n: number, label: string) {
  fireEvent.click(within(row(n)).getByLabelText("Collection"));
  fireEvent.click(await screen.findByRole("option", { name: label }));
}

describe("ObjectResourceGrants", () => {
  beforeEach(() => {
    h.listCollections.mockReset();
    h.listCollections.mockResolvedValue({
      collections: COLLECTIONS,
      page: { nextPageToken: "" },
    });
  });

  it("says no rows is no restriction", () => {
    render(<Harness />);
    expect(screen.getByText(OBJECT_GRANTS_UNRESTRICTED)).toBeInTheDocument();
  });

  it("reads the collections of the capability's tenant", async () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    await pickCollection(1, "docs");
    expect(h.listCollections.mock.calls[0][0].parent).toBe(`tenants/${TENANT}`);
  });

  it("asks for a collection, then shows the resource the row grants", async () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    expect(
      within(row(1)).getByLabelText("Collection"),
    ).toHaveAccessibleDescription("Choose a collection.");
    expect(within(row(1)).getByRole("status")).toHaveTextContent("—");

    // A nested collection is one segment, its "/" written ".".
    await pickCollection(1, `Q1 invoices (${NESTED})`);
    expect(
      screen.getByRole("status", { name: "Resource 1 grants" }),
    ).toHaveTextContent(`object://${TENANT}/invoices.2026.q1/`);

    fireEvent.change(within(row(1)).getByLabelText("Key prefix"), {
      target: { value: "contracts/" },
    });
    expect(
      screen.getByRole("status", { name: "Resource 1 grants" }),
    ).toHaveTextContent(`object://${TENANT}/invoices.2026.q1/contracts/`);
  });

  it("refuses a key prefix the key rule refuses, by the field", async () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    await pickCollection(1, "docs");
    const key = within(row(1)).getByLabelText("Key prefix");
    fireEvent.change(key, { target: { value: "a/../b" } });
    expect(key).toHaveAccessibleDescription(
      'The key prefix is refused: it holds a ".." segment.',
    );
    expect(key).toHaveAttribute("aria-invalid", "true");
  });

  it("refuses a row repeating an earlier one", async () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    await pickCollection(1, "docs");
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    await pickCollection(2, "docs");
    expect(
      within(row(2)).getByLabelText("Key prefix"),
    ).toHaveAccessibleDescription(DUPLICATE_GRANT);
  });

  it("removes the row asked for and keeps the rest", async () => {
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    await pickCollection(1, "docs");
    fireEvent.click(screen.getByRole("button", { name: "Add resource" }));
    await pickCollection(2, `Q1 invoices (${NESTED})`);

    fireEvent.click(screen.getByRole("button", { name: "Remove resource 1" }));
    expect(onChange).toHaveBeenLastCalledWith([
      { collection: NESTED, key: "" },
    ]);
    expect(
      screen.getByRole("status", { name: "Resource 1 grants" }),
    ).toHaveTextContent(`object://${TENANT}/invoices.2026.q1/`);
  });

  it("says when the collections could not be read", async () => {
    h.listCollections.mockRejectedValue(new Error("boom"));
    render(<Harness />);
    expect(await screen.findByText(/boom/)).toBeInTheDocument();
  });
});
