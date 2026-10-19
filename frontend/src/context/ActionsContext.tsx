"use client";

import React, {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  useCallback,
  useMemo,
  ReactNode,
} from "react";
import { useNotification } from "@/components/ui/Notification";

export interface Action {
  id: string;
  label: string;
  description?: string;
  icon?: React.ElementType;
  shortcut?: string;
  category: "navigation" | "operation" | "system" | "create";
  perform: () => Promise<void> | void;
  undo?: () => Promise<void> | void;
}

interface ActionsContextType {
  registerAction: (action: Action) => void;
  unregisterAction: (id: string) => void;
  actions: Action[];
  executeAction: (id: string) => Promise<void>;
  undoLastAction: () => Promise<void>;
  canUndo: boolean;
}

const ActionsContext = createContext<ActionsContextType | undefined>(undefined);

export function ActionsProvider({ children }: { children: ReactNode }) {
  const [actions, setActions] = useState<Action[]>([]);
  const [undoStack, setUndoStack] = useState<(() => Promise<void> | void)[]>(
    [],
  );
  const { showNotification } = useNotification();

  // A second registration under an id replaces the first, in its place: the
  // objects page's Undo is registered anew after every bulk trash, and must
  // restore the batch just trashed.
  const registerAction = useCallback((action: Action) => {
    setActions((prev) =>
      prev.some((a) => a.id === action.id)
        ? prev.map((a) => (a.id === action.id ? action : a))
        : [...prev, action],
    );
  }, []);

  const unregisterAction = useCallback((id: string) => {
    setActions((prev) => prev.filter((a) => a.id !== id));
  }, []);

  const undoLastAction = useCallback(async () => {
    const lastUndo = undoStack[undoStack.length - 1];
    if (!lastUndo) return;

    try {
      await lastUndo();
      setUndoStack((prev) => prev.slice(0, -1));
      showNotification({
        type: "success",
        title: "Action Reverted",
        message: "The last operation has been undone.",
      });
    } catch (err: unknown) {
      showNotification({
        type: "error",
        title: "Undo Failed",
        message: (err as Error).message,
      });
    }
  }, [undoStack, showNotification]);

  const executeAction = useCallback(
    async (id: string) => {
      const action = actions.find((a) => a.id === id);
      if (!action) return;

      try {
        await action.perform();

        if (action.undo) {
          setUndoStack((prev) => [...prev, action.undo!]);

          showNotification({
            type: "success",
            title: "Action Performed",
            message: `${action.label} completed.`,
            action: {
              label: "Undo",
              onClick: () => undoLastAction(),
            },
          });
        }
      } catch (err: unknown) {
        showNotification({
          type: "error",
          title: "Action Failed",
          message: (err as Error).message,
        });
      }
    },
    [actions, showNotification, undoLastAction],
  );

  // Memoized: an inline object literal here hands every consumer a
  // fresh reference on each provider render, re-rendering all their
  // subtrees even when nothing they read has changed.
  const value = useMemo(
    () => ({
      registerAction,
      unregisterAction,
      actions,
      executeAction,
      undoLastAction,
      canUndo: undoStack.length > 0,
    }),
    [
      registerAction,
      unregisterAction,
      actions,
      executeAction,
      undoLastAction,
      undoStack.length,
    ],
  );

  return (
    <ActionsContext.Provider value={value}>{children}</ActionsContext.Provider>
  );
}

/** The registry when one is mounted; a page rendered alone has none. */
function useActionsOptional() {
  return useContext(ActionsContext);
}

/** What a page offers the palette while it is on screen. */
export type PageAction = Omit<Action, "undo">;

/**
 * Offers `action` in the command palette while the calling page is mounted;
 * null offers nothing, as for a create the page has disabled. Selecting it
 * runs the page's latest `perform`, so it opens the same dialog the page's
 * own button does. Without an ActionsProvider it does nothing.
 */
export function usePageAction(action: PageAction | null) {
  const registry = useActionsOptional();
  const perform = useRef(action?.perform);
  useEffect(() => {
    perform.current = action?.perform;
  });
  const register = registry?.registerAction;
  const unregister = registry?.unregisterAction;
  const id = action?.id;
  const label = action?.label;
  const description = action?.description;
  const icon = action?.icon;
  const shortcut = action?.shortcut;
  const category = action?.category;
  useEffect(() => {
    if (!register || !unregister || !id || !label || !category) return;
    register({
      id,
      label,
      description,
      icon,
      shortcut,
      category,
      perform: () => perform.current?.(),
    });
    return () => unregister(id);
  }, [register, unregister, id, label, description, icon, shortcut, category]);
}

export function useActions() {
  const context = useContext(ActionsContext);
  if (!context) {
    throw new Error("useActions must be used within an ActionsProvider");
  }
  return context;
}
