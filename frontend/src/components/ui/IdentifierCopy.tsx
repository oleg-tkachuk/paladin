"use client";

import React from "react";
import { ClipboardIcon, CheckIcon } from "@heroicons/react/24/outline";
import { useNotification } from "@/components/ui/Notification";
import { cn } from "@/lib/utils";

interface IdentifierCopyProps {
  value: string;
  label: string;
  className?: string;
  iconOnly?: boolean;
}

export function IdentifierCopy({
  value,
  label,
  className,
  iconOnly,
}: IdentifierCopyProps) {
  const [copied, setCopied] = React.useState(false);
  const { showNotification } = useNotification();

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      showNotification({
        type: "success",
        title: "Copied",
        message: `${label} copied to clipboard.`,
      });
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error("Failed to copy", err);
    }
  };

  if (iconOnly) {
    return (
      <button
        type="button"
        onClick={handleCopy}
        className={cn(
          "p-2 rounded-xl transition-all active:scale-95 group/copy",
          copied
            ? "bg-emerald-500/10 text-emerald-500"
            : "bg-white/5 text-slate-500 hover:text-white hover:bg-white/10",
          className,
        )}
        title={`Copy ${label}`}
      >
        {copied ? (
          <CheckIcon className="w-4 h-4" />
        ) : (
          <ClipboardIcon className="w-4 h-4 group-hover/copy:scale-110 transition-transform" />
        )}
      </button>
    );
  }

  return (
    // A button, not a clickable div: a div cannot be reached or pressed from
    // the keyboard, so the identifier could only be copied with a mouse.
    <button
      type="button"
      onClick={handleCopy}
      title={`Copy ${label}`}
      className={cn(
        "group/copy relative flex w-full items-center justify-between p-4 text-left rounded-2xl bg-black/40 border border-white/5 hover:border-indigo-500/30 transition-all cursor-pointer overflow-hidden",
        className,
      )}
    >
      <span className="absolute inset-0 bg-indigo-500/[0.02] opacity-0 group-hover/copy:opacity-100 transition-opacity" />
      <span className="block space-y-1 min-w-0 pr-10">
        <span className="block text-xs font-semibold text-indigo-400/60 uppercase tracking-wider">
          {label}
        </span>
        <span className="block text-xs text-white font-mono truncate">
          {value}
        </span>
      </span>
      <span
        className={cn(
          "p-2 rounded-lg transition-all",
          copied
            ? "text-emerald-500"
            : "text-slate-500 group-hover/copy:text-white",
        )}
      >
        {copied ? (
          <CheckIcon className="w-4 h-4" />
        ) : (
          <ClipboardIcon className="w-4 h-4" />
        )}
      </span>
    </button>
  );
}
