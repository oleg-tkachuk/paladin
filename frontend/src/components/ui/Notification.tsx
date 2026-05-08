"use client";

/**
 * Compatibility shim that preserves the legacy useNotification() API
 * (showNotification / hideNotification) but renders through sonner.
 *
 * Existing call sites:
 *   const { showNotification } = useNotification();
 *   showNotification({ type: "success", title: "Done", message: "…" });
 *
 * No call site changes are required — the new design just looks better.
 */

import React, { createContext, useContext, useMemo, useRef } from "react";
import { toast } from "sonner";

export type NotificationType = "success" | "error" | "info" | "warning";

export interface NotificationAction {
  label: string;
  onClick: () => void;
}

export interface NotificationErrorDetails {
  code?: string;
  status?: number;
  trace?: string;
  rawError?: string;
}

export interface Notification {
  id: string;
  type: NotificationType;
  title: string;
  message?: string;
  duration?: number;
  action?: NotificationAction;
  errorDetails?: NotificationErrorDetails;
}

interface NotificationContextType {
  notifications: Notification[];
  showNotification: (notification: Omit<Notification, "id">) => void;
  hideNotification: (id: string) => void;
}

const NotificationContext = createContext<NotificationContextType | undefined>(
  undefined,
);

const newId = () => Math.random().toString(36).slice(2, 9);

export function NotificationProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  // Track active toast IDs so callers can dismiss programmatically.
  const idMapRef = useRef<Map<string, string | number>>(new Map());

  const value = useMemo<NotificationContextType>(
    () => ({
      notifications: [],
      showNotification: (n) => {
        const localId = newId();
        const opts: Parameters<typeof toast>[1] = {
          description: n.message,
          duration: n.duration ?? 5000,
        };
        if (n.action) {
          opts.action = { label: n.action.label, onClick: n.action.onClick };
        }
        let toastId: string | number;
        switch (n.type) {
          case "success":
            toastId = toast.success(n.title, opts);
            break;
          case "error":
            toastId = toast.error(n.title, opts);
            break;
          case "warning":
            toastId = toast.warning(n.title, opts);
            break;
          case "info":
          default:
            toastId = toast.info(n.title, opts);
            break;
        }
        idMapRef.current.set(localId, toastId);
      },
      hideNotification: (id) => {
        const toastId = idMapRef.current.get(id);
        if (toastId !== undefined) {
          toast.dismiss(toastId);
          idMapRef.current.delete(id);
        }
      },
    }),
    [],
  );

  return (
    <NotificationContext.Provider value={value}>
      {children}
    </NotificationContext.Provider>
  );
}

export function useNotification() {
  const ctx = useContext(NotificationContext);
  if (!ctx) {
    throw new Error(
      "useNotification must be used within a NotificationProvider",
    );
  }
  return ctx;
}
