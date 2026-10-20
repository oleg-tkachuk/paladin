import userEvent from "@testing-library/user-event";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const nav = vi.hoisted(() => ({ search: "" }));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
}));

import { DetailPanel } from "./DetailPanel";
import { openableRow } from "./RecordDialog";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import type { TableUrl } from "@/lib/table/urlState";

const TABS = ["summary", "diff"] as const;
const GRANTS: TableUrl = { namespace: "grants" };
const ROWS = ["r1", "r2", "r3"];

function navigate(search: string) {
  window.history.replaceState(null, "", `/list${search}`);
  nav.search = search;
}

const search = () => new URLSearchParams(window.location.search);

function List({ url }: { url?: TableUrl }) {
  const panel = useDetailPanel(TABS, url);
  return (
    <>
      <button type="button">Elsewhere</button>
      <table>
        <tbody>
          {ROWS.map((id) => (
            <tr
              key={id}
              {...openableRow(() => panel.open(id))}
              {...panel.rowProps(id)}
            >
              <td>Row {id}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <DetailPanel
        open={panel.openId !== null}
        onClose={panel.close}
        rowId={panel.openId}
        title="Entry"
        identifier={panel.openId ?? undefined}
        tab={panel.tab}
        onTabChange={(t) => panel.setTab(t as (typeof TABS)[number])}
        tabs={[
          {
            value: "summary",
            label: "Summary",
            content: <p>Summary of {panel.openId}</p>,
          },
          {
            value: "diff",
            label: "Diff",
            content: <p>Diff of {panel.openId}</p>,
          },
        ]}
        footer={<button type="button">Act</button>}
      />
    </>
  );
}

beforeEach(() => navigate(""));

describe("DetailPanel", () => {
  it("opens a row's details beside the list, and puts the row in the URL", async () => {
    render(<List />);
    await userEvent.click(screen.getByText("Row r2"));
    const panel = await screen.findByRole("dialog", { name: "Entry" });
    expect(panel).toHaveTextContent("Summary of r2");
    // The list stays on the page and usable: nothing hides it from a reader.
    expect(screen.getByText("Row r1")).toBeVisible();
    expect(search().get("id")).toBe("r2");
    // The first tab is the default, and stays out of the URL.
    expect(search().has("tab")).toBe(false);
    expect(screen.getByRole("button", { name: "Act" })).toBeInTheDocument();
    expect(screen.getByText("r2")).toBeInTheDocument();
  });

  it("keeps the tab in the URL", async () => {
    render(<List />);
    await userEvent.click(screen.getByText("Row r1"));
    await userEvent.click(await screen.findByRole("tab", { name: "Diff" }));
    expect(screen.getByText("Diff of r1")).toBeInTheDocument();
    await waitFor(() => expect(search().get("tab")).toBe("diff"));
  });

  it("opens from a link on the tab the link names", async () => {
    navigate("?id=r3&tab=diff");
    render(<List />);
    expect(await screen.findByText("Diff of r3")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Diff" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("reads a tab it does not have as its first", async () => {
    navigate("?id=r3&tab=nonsense");
    render(<List />);
    expect(await screen.findByText("Summary of r3")).toBeInTheDocument();
  });

  it("closes on Escape, clears the URL and gives focus back to the row", async () => {
    navigate("?id=r2&tab=diff");
    render(<List />);
    await screen.findByRole("dialog");
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(search().has("id")).toBe(false);
    expect(search().has("tab")).toBe(false);
    expect(screen.getByText("Row r2").closest("tr")).toHaveFocus();
  });

  it("moves to another row on a click, rather than closing", async () => {
    render(<List />);
    await userEvent.click(screen.getByText("Row r1"));
    await screen.findByText("Summary of r1");
    await userEvent.click(screen.getByText("Row r3"));
    expect(await screen.findByText("Summary of r3")).toBeInTheDocument();
    expect(search().get("id")).toBe("r3");
  });

  it("closes on a click elsewhere, leaving focus where it landed", async () => {
    render(<List />);
    await userEvent.click(screen.getByText("Row r1"));
    await screen.findByRole("dialog");
    const elsewhere = screen.getByRole("button", { name: "Elsewhere" });
    await userEvent.click(elsewhere);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(search().has("id")).toBe(false);
    expect(elsewhere).toHaveFocus();
  });

  it("keeps a namespaced table's panel under its own parameters", async () => {
    render(<List url={GRANTS} />);
    await userEvent.click(screen.getByText("Row r1"));
    await userEvent.click(await screen.findByRole("tab", { name: "Diff" }));
    await waitFor(() => expect(search().get("grants.tab")).toBe("diff"));
    expect(search().get("grants.id")).toBe("r1");
    expect(search().has("id")).toBe(false);
  });
});
