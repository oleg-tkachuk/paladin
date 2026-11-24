import { describe, expect, it } from "vitest";

import { exportColumns, type TableColumns } from "./columns";

type Row = { id: string; name: string };
const COLUMNS: TableColumns<Row, "name" | "id" | "actions"> = {
  name: { header: "Name", text: (r) => r.name },
  id: { header: "ID", text: (r) => r.id },
  actions: { header: "Actions" },
};

describe("exportColumns", () => {
  it("keeps the columns holding text, in the table's order", () => {
    expect(exportColumns(COLUMNS).map((c) => c.header)).toEqual(["Name", "ID"]);
  });

  it("leaves out the columns the user hid", () => {
    expect(exportColumns(COLUMNS, ["name"]).map((c) => c.header)).toEqual([
      "ID",
    ]);
  });

  it("reads a row through the column's own text", () => {
    const [name] = exportColumns(COLUMNS);
    expect(name.text({ id: "1", name: "alice" })).toBe("alice");
  });
});
