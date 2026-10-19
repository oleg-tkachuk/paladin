"use client";

// The frame of a settings page: its header, a summary of whose settings
// they are, then the sections, side by side where the screen allows.
import type React from "react";

import { PAGE_STACK } from "./ListPage";

export function SettingsPage({
  header,
  summary,
  children,
}: {
  header: React.ReactNode;
  /** Whose settings: the account, the tenant. */
  summary?: React.ReactNode;
  /** The sections, each a card with its own save. */
  children: React.ReactNode;
}) {
  return (
    <div className={PAGE_STACK}>
      {header}
      {summary}
      <div className="grid gap-6 lg:grid-cols-2">{children}</div>
    </div>
  );
}
