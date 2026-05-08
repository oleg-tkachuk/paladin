"use client";

import React, {
  createContext,
  useContext,
  useState,
  useCallback,
  ReactNode,
} from "react";
import { useNotification } from "@/components/ui/Notification";

export interface Action {
  id: string;
  label: string;
  description?: string;
  icon?: React.ElementType;
  shortcut?: string;
  category: "navigation" | "operation" | "system";
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

  const registerAction = useCallback((action: Action) => {
    setActions((prev) => {
      if (prev.find((a) => a.id === action.id)) return prev;
      return [...prev, action];
    });
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

  return (
    <ActionsContext.Provider
      value={{
        registerAction,
        unregisterAction,
        actions,
        executeAction,
        undoLastAction,
        canUndo: undoStack.length > 0,
      }}
    >
      {children}
    </ActionsContext.Provider>
  );
}

export function useActions() {
  const context = useContext(ActionsContext);
  if (!context) {
    throw new Error("useActions must be used within an ActionsProvider");
  }
  return context;
}
