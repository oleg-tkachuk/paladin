"use client";

import { createContext, useCallback, useContext, useState } from "react";
import type React from "react";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import { useBumpRefresh } from "@/context/RefreshContext";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import { objectClient } from "@/lib/connect/client";

/** The label of the toast's way back. */
export const UNDO_LABEL = "Undo";

type OfferUndo = (trashed: Object$ | undefined) => void;

const UndoTrashContext = createContext<OfferUndo | null>(null);

/**
 * The way back from a move to the trash: the toast that says it happened
 * carries an Undo, and the Undo restores the very object the trash holds —
 * through the confirm every restore in the console takes. A restore of an
 * object that changed in the meantime is refused by the server, and says so.
 *
 * Mounted once in the shell, not by the page that trashed: the inspector
 * closes and the object page navigates away right after a trash, and an Undo
 * held by either would go with it.
 */
export function UndoTrashProvider({ children }: { children: React.ReactNode }) {
  const { showNotification } = useNotification();
  const bumpRefresh = useBumpRefresh();
  const [pending, setPending] = useState<Object$ | null>(null);

  const offerUndo = useCallback(
    (trashed: Object$ | undefined) => {
      if (!trashed) return;
      const name = trashed.key.split("/").pop() || trashed.key;
      showNotification({
        type: "success",
        title: "Moved to Trash",
        message: `${name} is in the trash.`,
        action: { label: UNDO_LABEL, onClick: () => setPending(trashed) },
      });
    },
    [showNotification],
  );

  const restore = async () => {
    if (!pending) return;
    const object = pending;
    setPending(null);
    try {
      await objectClient.restoreObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
      });
      bumpRefresh("objects");
      showNotification({
        type: "success",
        title: "Restored",
        message: `${object.key} is back.`,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Restore failed",
        message: (err as Error).message,
      });
    }
  };

  const undoConfirm = (
    <ConfirmModal
      isOpen={pending !== null}
      onClose={() => setPending(null)}
      onConfirm={restore}
      type="warning"
      title="Restore the object?"
      message={`${pending?.key ?? ""} returns to the collection and can be read again.`}
      warning="A restore is refused for an object that changed since it was trashed."
      confirmText="Restore"
    />
  );

  return (
    <UndoTrashContext.Provider value={offerUndo}>
      {children}
      {undoConfirm}
    </UndoTrashContext.Provider>
  );
}

/**
 * Says an object went to the trash, with an Undo where the shell provides
 * one; outside it, the toast alone.
 */
export function useUndoTrash(): { offerUndo: OfferUndo } {
  const provided = useContext(UndoTrashContext);
  const { showNotification } = useNotification();
  const plain = useCallback<OfferUndo>(
    (trashed) => {
      if (!trashed) return;
      showNotification({
        type: "success",
        title: "Moved to Trash",
        message: `${trashed.key.split("/").pop() || trashed.key} is in the trash.`,
      });
    },
    [showNotification],
  );
  return { offerUndo: provided ?? plain };
}
