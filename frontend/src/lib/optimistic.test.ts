import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import { edit, optimistically } from "./optimistic";

type Row = { name: string; on: boolean };
const LIST = ["rows"] as const;
const rows: Row[] = [
  { name: "a", on: false },
  { name: "b", on: false },
];
const switchOn = (name: string) =>
  edit<Row[]>(LIST, (list) =>
    list.map((r) => (r.name === name ? { ...r, on: true } : r)),
  );

/** A client holding the rows, whose re-read answers `server`. */
function client(server: () => Row[] = () => rows) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Fresh until invalidated, so only the change's own re-read asks the server.
  qc.setQueryDefaults(LIST, { queryFn: () => server(), staleTime: Infinity });
  qc.setQueryData(LIST, rows);
  return qc;
}

/** Keeps the query observed, so an invalidation reads it again. */
function observe(qc: QueryClient) {
  return new QueryObserver(qc, { queryKey: LIST });
}

describe("optimistically", () => {
  it("shows the change before the server answers", async () => {
    const qc = client();
    let answer!: () => void;
    const pending = optimistically(
      qc,
      [switchOn("a")],
      () => new Promise<void>((r) => (answer = r)),
    );
    await vi.waitFor(() =>
      expect(qc.getQueryData<Row[]>(LIST)?.[0].on).toBe(true),
    );
    expect(qc.getQueryData<Row[]>(LIST)?.[1].on).toBe(false);
    answer();
    await pending;
  });

  it("puts the data back when the server refuses, and throws on", async () => {
    const qc = client();
    const refused = new Error("aborted");
    await expect(
      optimistically(qc, [switchOn("a")], () => Promise.reject(refused)),
    ).rejects.toBe(refused);
    expect(qc.getQueryData(LIST)).toEqual(rows);
  });

  it("edits every cached query under the key, and puts back each", async () => {
    const qc = client();
    const page2 = [...LIST, "page-2"];
    qc.setQueryData(page2, [{ name: "a", on: false }]);
    await expect(
      optimistically(qc, [switchOn("a")], async () => {
        expect(qc.getQueryData<Row[]>(page2)?.[0].on).toBe(true);
        throw new Error("no");
      }),
    ).rejects.toThrow("no");
    expect(qc.getQueryData<Row[]>(page2)?.[0].on).toBe(false);
  });

  // A page whose first read is still in flight has a query with no data yet.
  it("leaves a query with no data yet alone", async () => {
    const qc = client();
    const other = ["detail", "a"];
    qc.getQueryCache().build(qc, { queryKey: other });
    const apply = vi.fn((r: Row) => ({ ...r, on: true }));
    await optimistically(qc, [edit<Row>(other, apply)], async () => "done");
    expect(apply).not.toHaveBeenCalled();
    expect(qc.getQueryData(other)).toBeUndefined();
  });

  it("reads the queries again either way, ending as the server has them", async () => {
    const server = vi.fn(() => [{ name: "a", on: true }]);
    const qc = client(server);
    const unsubscribe = observe(qc).subscribe(() => {});
    await optimistically(qc, [switchOn("a")], async () => undefined);
    await vi.waitFor(() => expect(server).toHaveBeenCalledTimes(1));
    await expect(
      optimistically(qc, [switchOn("b")], () => Promise.reject(new Error())),
    ).rejects.toThrow();
    await vi.waitFor(() => expect(server).toHaveBeenCalledTimes(2));
    expect(qc.getQueryData(LIST)).toEqual([{ name: "a", on: true }]);
    unsubscribe();
  });

  it("answers without waiting for the re-read", async () => {
    const qc = client(() => new Promise<Row[]>(() => {}) as unknown as Row[]);
    const unsubscribe = observe(qc).subscribe(() => {});
    await expect(
      optimistically(qc, [switchOn("a")], async () => "done"),
    ).resolves.toBe("done");
    await expect(
      optimistically(qc, [switchOn("a")], () =>
        Promise.reject(new Error("no")),
      ),
    ).rejects.toThrow("no");
    unsubscribe();
  });

  it("returns what the change answered", async () => {
    await expect(
      optimistically(client(), [switchOn("a")], async () => 42),
    ).resolves.toBe(42);
  });

  // A read already in flight would land after the edit and wipe it out.
  it("cancels a read in flight before editing", async () => {
    const qc = client();
    const cancel = vi.spyOn(qc, "cancelQueries");
    await optimistically(qc, [switchOn("a")], async () => undefined);
    expect(cancel).toHaveBeenCalledWith({ queryKey: LIST });
  });
});
