"use client";

import Link from "next/link";
import type { ReactNode } from "react";

import { useShell } from "@/context/ShellContext";
import {
  TooltipContent,
  TooltipRoot,
  TooltipTrigger,
} from "@/components/ui/Tooltip";
import { formatTimestampUTC, NO_TIMESTAMP } from "@/lib/format/timestamp";
import {
  formatBuildLabel,
  isBuildSkew,
  uiBuildInfo,
} from "@/lib/ui/build-info";

/** Where the version links: the page that lists every role's build. */
const HEALTH_PATH = "/health";

interface BuildVersionProps {
  /** Shown while the backend's version is loading or unavailable. */
  fallback: ReactNode;
}

/**
 * The running release under the product name: `v11.9.0 (a1b2c3d)`.
 *
 * It is the backend's build — what an operator quotes in an incident or
 * matches against a release — read from the shell's GetVersion section. The
 * console's own build sits in the tooltip beside it, and a warning dot marks
 * the two being built from different commits: a stale cached bundle, or a
 * rollout that moved only one half.
 */
export function BuildVersion({ fallback }: BuildVersionProps) {
  const { version } = useShell();
  if (version.status !== "ok" || !version.data) return <>{fallback}</>;

  const backend = version.data;
  const ui = uiBuildInfo();
  const label = formatBuildLabel(backend.version, backend.commit);
  const skew = isBuildSkew(backend.commit, ui.commit);
  const builtAt = formatTimestampUTC(backend.buildTime);

  return (
    <TooltipRoot>
      <TooltipTrigger asChild>
        <Link
          href={HEALTH_PATH}
          aria-label={`Paladin ${label}${skew ? ", console built from a different commit" : ""}`}
          className="flex min-w-0 items-center gap-1.5 rounded-sm font-mono text-xs text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {skew ? (
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full bg-warning"
            />
          ) : null}
          <span className="truncate">{label}</span>
        </Link>
      </TooltipTrigger>
      <TooltipContent side="bottom" align="start">
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
          <dt className="text-muted-foreground">Backend</dt>
          <dd className="font-mono">{label}</dd>
          {builtAt !== NO_TIMESTAMP ? (
            <>
              <dt className="text-muted-foreground">Built</dt>
              <dd className="font-mono">{builtAt}</dd>
            </>
          ) : null}
          <dt className="text-muted-foreground">Console</dt>
          <dd className="font-mono">
            {formatBuildLabel(ui.version, ui.commit)}
          </dd>
        </dl>
        {skew ? (
          <p className="mt-1.5 text-warning">
            The console and the backend were built from different commits.
          </p>
        ) : null}
      </TooltipContent>
    </TooltipRoot>
  );
}
