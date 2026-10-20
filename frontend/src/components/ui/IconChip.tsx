import type React from "react";

import { cn } from "@/lib/utils";

/**
 * An icon on a tinted chip: what a card title, or a tile that links to a
 * section, is marked with. One shape, a tone per meaning, so a page never
 * hand-rolls its own square of colour.
 */
export const IconTone = {
  Accent: "accent",
  Success: "success",
  Warning: "warning",
  Info: "info",
  Danger: "danger",
  Neutral: "neutral",
} as const;
export type IconTone = (typeof IconTone)[keyof typeof IconTone];

/**
 * Each tone's chip: the surface it paints and the ink the icon takes.
 * themeContrast.test.ts holds the ink to 3:1 on its chip, on a card, in
 * every palette.
 */
export const ICON_CHIP_TONE: Record<
  IconTone,
  { surface: string; ink: string }
> = {
  accent: { surface: "bg-primary/15 ring-primary/25", ink: "text-primary" },
  success: { surface: "bg-success/15 ring-success/25", ink: "text-success" },
  warning: { surface: "bg-warning/15 ring-warning/25", ink: "text-warning" },
  info: { surface: "bg-info/15 ring-info/25", ink: "text-info" },
  danger: {
    surface: "bg-destructive/15 ring-destructive/25",
    ink: "text-destructive",
  },
  neutral: { surface: "bg-muted ring-border", ink: "text-muted-foreground" },
};

/** Chip sizes: sm beside a small title, md beside a section's, lg on a tile. */
const SIZE = {
  sm: { chip: "size-6 rounded-md", icon: "size-3.5" },
  md: { chip: "size-8 rounded-lg", icon: "size-4" },
  lg: { chip: "size-9 rounded-lg", icon: "size-5" },
} as const;
export type IconChipSize = keyof typeof SIZE;

export function IconChip({
  icon: Icon,
  tone = IconTone.Accent,
  size = "sm",
  className,
}: {
  icon: React.ElementType;
  tone?: IconTone;
  size?: IconChipSize;
  className?: string;
}) {
  const t = ICON_CHIP_TONE[tone];
  return (
    <span
      aria-hidden
      data-slot="icon-chip"
      data-tone={tone}
      className={cn(
        "inline-flex shrink-0 items-center justify-center ring-1 ring-inset",
        SIZE[size].chip,
        t.surface,
        t.ink,
        className,
      )}
    >
      <Icon className={SIZE[size].icon} />
    </span>
  );
}

/**
 * The tone of a card that counts problems: danger while anything is at
 * fault, warning while something only nears it, success when nothing does.
 */
export function alertTone(critical: number, warning = 0): IconTone {
  if (critical > 0) return IconTone.Danger;
  if (warning > 0) return IconTone.Warning;
  return IconTone.Success;
}
