import type React from "react";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  PauseCircleIcon,
  QuestionMarkCircleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

// One vocabulary for the state of anything the console lists: a backend, a
// bucket's provisioning, a health component, a quota, an operation. After
// Argo CD's, where every status is a colour *and* a glyph, so a state never
// rests on colour alone and reads the same on every page.

export const StatusTone = {
  /** Working as it should: healthy, active, succeeded. */
  Success: "success",
  /** A change under way: provisioning, draining, a migration, running. */
  Progress: "progress",
  /** Needs a look but still works: degraded, near a limit. */
  Warning: "warning",
  /** Broken or refused: unhealthy, failed, revoked. */
  Danger: "danger",
  /** Paused on purpose: maintenance, disabled, frozen. Not a fault. */
  Suspended: "suspended",
  /** Nobody has said: unreachable, not yet reported. */
  Unknown: "unknown",
} as const;
export type StatusTone = (typeof StatusTone)[keyof typeof StatusTone];

/**
 * Each tone's ink (text and glyph), its solid fill (a dot, a bar segment)
 * and its glyph. themeContrast.test.ts holds every ink to 4.5:1 on the
 * console's surfaces.
 */
export const STATUS_TONE: Record<
  StatusTone,
  { ink: string; fill: string; icon: React.ElementType }
> = {
  success: { ink: "text-success", fill: "bg-success", icon: CheckCircleIcon },
  progress: { ink: "text-progress", fill: "bg-progress", icon: ArrowPathIcon },
  warning: {
    ink: "text-warning",
    fill: "bg-warning",
    icon: ExclamationTriangleIcon,
  },
  danger: {
    ink: "text-destructive",
    fill: "bg-destructive",
    icon: XCircleIcon,
  },
  suspended: {
    ink: "text-suspended",
    fill: "bg-suspended",
    icon: PauseCircleIcon,
  },
  unknown: {
    ink: "text-unknown",
    fill: "bg-unknown",
    icon: QuestionMarkCircleIcon,
  },
};

/** The order a tally reads in: what needs attention first. */
export const STATUS_ORDER: readonly StatusTone[] = [
  StatusTone.Danger,
  StatusTone.Warning,
  StatusTone.Progress,
  StatusTone.Suspended,
  StatusTone.Unknown,
  StatusTone.Success,
];

/** The badge variant that carries a tone. */
export const STATUS_BADGE: Record<
  StatusTone,
  "success" | "progress" | "warning" | "destructive" | "suspended" | "unknown"
> = {
  success: "success",
  progress: "progress",
  warning: "warning",
  danger: "destructive",
  suspended: "suspended",
  unknown: "unknown",
};
