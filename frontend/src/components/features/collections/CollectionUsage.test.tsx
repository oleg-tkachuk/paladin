import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { render, screen } from "@/test/utils";

import {
  CollectionSchema,
  CollectionUsageSchema,
} from "@/gen/paladin/admin/v1/types_pb";

import { UsageCells } from "./CollectionUsage";

const row = (c: ReturnType<typeof create<typeof CollectionSchema>>) =>
  render(
    <table>
      <tbody>
        <tr>
          <UsageCells collection={c} />
        </tr>
      </tbody>
    </table>,
  );

describe("UsageCells", () => {
  it("shows the objects, their size and when the newest was stored", () => {
    const at = BigInt(Math.floor(Date.now() / 1000) - 3 * 3600);
    row(
      create(CollectionSchema, {
        collection: "docs",
        usage: create(CollectionUsageSchema, {
          objectCount: 1234n,
          totalBytes: 2048n,
          lastCommittedAt: { seconds: at, nanos: 0 },
        }),
      }),
    );
    expect(screen.getByText((1234).toLocaleString())).toBeInTheDocument();
    expect(screen.getByText("2 KB")).toBeInTheDocument();
    expect(screen.getByText("3h ago")).toHaveAttribute("title");
  });

  it("says an empty collection holds nothing", () => {
    row(create(CollectionSchema, { collection: "empty" }));
    expect(screen.getByText("0")).toBeInTheDocument();
    expect(screen.getAllByText("—")).toHaveLength(2);
  });
});
