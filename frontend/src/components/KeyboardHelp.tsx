"use client";

// KeyboardHelp — global modal (Shift+?) listing every keyboard
// shortcut on the platform. Mounted once at the root layout; it
// listens for the ? key globally and pops itself.
//
// New shortcuts on this app should be added to SHORTCUTS below
// (keep the source of truth + the help dialog in one place).

import React, { useEffect, useState } from "react";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

interface Shortcut {
  keys: string[];
  desc: string;
}

const SHORTCUTS: { group: string; items: Shortcut[] }[] = [
  {
    group: "Navigation",
    items: [
      { keys: ["?"], desc: "Show this help" },
      { keys: ["⌘", "K"], desc: "Open command palette" },
      { keys: ["g", "d"], desc: "Go to dashboard" },
      { keys: ["g", "t"], desc: "Go to tenants" },
      { keys: ["g", "b"], desc: "Go to buckets" },
      { keys: ["g", "s"], desc: "Go to storage backends" },
    ],
  },
  {
    group: "Tables",
    items: [
      { keys: ["j", "↓"], desc: "Move down a row" },
      { keys: ["k", "↑"], desc: "Move up a row" },
      { keys: ["g", "g"], desc: "Jump to first row" },
      { keys: ["⇧", "G"], desc: "Jump to last row" },
      { keys: ["Enter"], desc: "Open active row" },
      { keys: ["Esc"], desc: "Clear selection" },
      { keys: ["/"], desc: "Focus search" },
    ],
  },
  {
    group: "File browser",
    items: [
      { keys: ["x"], desc: "Toggle row selection" },
      { keys: ["⇧", "X"], desc: "Range-select" },
      { keys: ["u"], desc: "Upload (file picker)" },
      { keys: ["Delete"], desc: "Delete selected" },
    ],
  },
];

export function KeyboardHelp() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "?" && e.shiftKey && !e.metaKey && !e.ctrlKey) {
        const t = e.target as HTMLElement | null;
        if (
          t &&
          (t.tagName === "INPUT" ||
            t.tagName === "TEXTAREA" ||
            t.isContentEditable)
        )
          return;
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="grid grid-cols-1 gap-6 py-2 md:grid-cols-2">
          {SHORTCUTS.map((g) => (
            <div key={g.group} className="space-y-2">
              <h3 className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                {g.group}
              </h3>
              <ul className="space-y-1.5">
                {g.items.map((s) => (
                  <li
                    key={s.desc}
                    className="flex items-center justify-between gap-3 text-xs"
                  >
                    <span className="text-muted-foreground">{s.desc}</span>
                    <span className="flex shrink-0 gap-1">
                      {s.keys.map((k, i) => (
                        <kbd
                          key={i}
                          className={cn(
                            "inline-flex h-5 min-w-[1.25rem] items-center justify-center rounded border border-border bg-muted px-1 font-mono text-[10px]",
                          )}
                        >
                          {k}
                        </kbd>
                      ))}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
