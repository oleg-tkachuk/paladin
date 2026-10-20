// Which of a selection can move to the trash. An object still PENDING is an
// upload that never completed: there is nothing to trash, and the server
// refuses it, so the console leaves it out and says so rather than reporting
// it moved.

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";

export interface TrashSplit<T> {
  /** What the delete is sent for. */
  trash: T[];
  /** Uploads that have not completed, left where they are. */
  unfinished: T[];
}

export function splitForTrash<T extends { state: ObjectState }>(
  objects: readonly T[],
): TrashSplit<T> {
  return {
    trash: objects.filter((o) => o.state !== ObjectState.PENDING),
    unfinished: objects.filter((o) => o.state === ObjectState.PENDING),
  };
}

/** The title when nothing selected can move to the trash. */
export const NOTHING_TO_TRASH = "Nothing to move to the trash";

/** Why the unfinished uploads stay, for a count of them. */
export function unfinishedNote(count: number): string {
  const which =
    count === 1
      ? "1 upload has not completed and stays as it is"
      : `${count} uploads have not completed and stay as they are`;
  return `${which}; the worker discards an unfinished upload some time after its upload URL expires.`;
}
