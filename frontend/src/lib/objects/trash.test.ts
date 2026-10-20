import { describe, expect, it } from "vitest";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";

import { splitForTrash, unfinishedNote } from "./trash";

const obj = (id: string, state: ObjectState) => ({ id, state });

describe("splitForTrash", () => {
  it("leaves unfinished uploads out of the delete", () => {
    const { trash, unfinished } = splitForTrash([
      obj("a", ObjectState.AVAILABLE),
      obj("p", ObjectState.PENDING),
      obj("d", ObjectState.DELETED),
    ]);
    expect(trash.map((o) => o.id)).toEqual(["a", "d"]);
    expect(unfinished.map((o) => o.id)).toEqual(["p"]);
  });

  it("an empty selection is empty both ways", () => {
    expect(splitForTrash([])).toEqual({ trash: [], unfinished: [] });
  });
});

describe("unfinishedNote", () => {
  it("counts in the singular and the plural", () => {
    // One upload "stays as it is"; it read "stays as they are".
    expect(unfinishedNote(1)).toMatch(
      /^1 upload has not completed and stays as it is; /,
    );
    expect(unfinishedNote(3)).toMatch(
      /^3 uploads have not completed and stay as they are; /,
    );
  });
});
