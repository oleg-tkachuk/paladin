import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

import { BYTE_DECIMALS, bytesParts } from "@/lib/format/bytes";

// The text sizes globals.css adds to Tailwind's scale (`--text-<name>`).
// tailwind-merge has to be told any it gets: a size it does not know
// reads as a text colour, so `text-tiny text-muted-foreground` kept the colour,
// dropped the size, and the text rendered at whatever it inherited.
export const CUSTOM_TEXT_SIZES: string[] = ["display"];

const twMerge = extendTailwindMerge({
  extend: { theme: { text: CUSTOM_TEXT_SIZES } },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Converts a protobuf Timestamp (plain object) to a JS Date.
 */
export function timestampToDate(
  ts: { seconds?: number | bigint; nanos?: number } | null | undefined,
): Date {
  if (!ts) return new Date();
  const seconds = Number(ts.seconds || 0);
  const nanos = Number(ts.nanos || 0);
  return new Date(seconds * 1000 + nanos / 1000000);
}

export function formatDate(date: Date | undefined): string {
  if (!date) return "N/A";
  const now = new Date();
  const diff = now.getTime() - date.getTime();
  const seconds = Math.floor(diff / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) return `${days}d ago`;
  if (hours > 0) return `${hours}h ago`;
  if (minutes > 0) return `${minutes}m ago`;
  return `${seconds}s ago`;
}

/** A size as one string: "1.5 KB". Bytes renders it with its exact count. */
export function formatBytes(
  bytes: number | bigint,
  decimals: number = BYTE_DECIMALS,
) {
  const { value, unit } = bytesParts(bytes, decimals);
  return `${value} ${unit}`;
}

/**
 * Copy text to clipboard with fallback for non-secure contexts.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
    // Fallback for non-secure contexts (e.g., http://paladin.local)
    const textArea = document.createElement("textarea");
    textArea.value = text;
    textArea.style.position = "fixed";
    textArea.style.left = "-9999px";
    document.body.appendChild(textArea);
    textArea.focus();
    textArea.select();
    try {
      document.execCommand("copy");
      return true;
    } finally {
      textArea.remove();
    }
  } catch {
    return false;
  }
}
