import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// The text sizes globals.css adds to Tailwind's scale (`--text-<name>`); none
// today. tailwind-merge has to be told any it gets: a size it does not know
// reads as a text colour, so `text-tiny text-muted-foreground` kept the colour,
// dropped the size, and the text rendered at whatever it inherited.
export const CUSTOM_TEXT_SIZES: string[] = [];

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

export function formatBytes(bytes: number | bigint, decimals = 2) {
  const n = typeof bytes === "bigint" ? Number(bytes) : bytes;
  if (n === 0) return "0 Bytes";
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ["Bytes", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"];
  const i = Math.floor(Math.log(n) / Math.log(k));
  return parseFloat((n / Math.pow(k, i)).toFixed(dm)) + " " + sizes[i];
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
