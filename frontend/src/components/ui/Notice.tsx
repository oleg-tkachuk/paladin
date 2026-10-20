"use client";

// A notice on screen: across a page as a banner, or beside a list row as a
// glyph whose tooltip reads it. Its severity takes the console's status
// tokens — info the info ink, warning the warning tone, critical the danger
// tone — with the tone's glyph, so it reads without colour as well.
import type React from "react";
import Link from "next/link";
import {
  ArrowTopRightOnSquareIcon,
  InformationCircleIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  TooltipContent,
  TooltipRoot,
  TooltipTrigger,
} from "@/components/ui/Tooltip";
import { useNoticeDismissal } from "@/hooks/useNoticeDismissal";
import type { Notice, NoticeSeverity } from "@/lib/notice";
import { FOCUS_RING } from "@/lib/ui/states";
import { STATUS_TONE } from "@/lib/ui/status";
import { cn } from "@/lib/utils";

const NOTICE_TONE: Record<
  NoticeSeverity,
  { label: string; ink: string; surface: string; icon: React.ElementType }
> = {
  info: {
    label: "Notice",
    ink: "text-info",
    surface: "border-info/40 bg-info/10",
    icon: InformationCircleIcon,
  },
  warning: {
    label: "Warning",
    ink: STATUS_TONE.warning.ink,
    surface: "border-warning/40 bg-warning/10",
    icon: STATUS_TONE.warning.icon,
  },
  critical: {
    label: "Critical",
    ink: STATUS_TONE.danger.ink,
    surface: "border-destructive/40 bg-destructive/10",
    icon: STATUS_TONE.danger.icon,
  },
};

/** Where the notice points: a page of this console, or another site. */
function NoticeLink({ url }: { url: string }) {
  const className = "inline-flex items-center gap-1 text-primary underline";
  return url.startsWith("/") ? (
    <Link href={url} className={className}>
      Read more
    </Link>
  ) : (
    <a href={url} target="_blank" rel="noreferrer" className={className}>
      Read more
      <ArrowTopRightOnSquareIcon className="size-3.5" aria-hidden />
    </a>
  );
}

/**
 * A notice across the page. The reader can dismiss it unless it is
 * permanent, and a dismissed one stays gone until what it says changes.
 * `className` places it: a strip under the top bar, a box under a header.
 */
export function NoticeBanner({
  notice,
  className,
}: {
  notice: Notice;
  className?: string;
}) {
  const { dismissed, dismiss } = useNoticeDismissal(notice);
  if (dismissed) return null;
  const tone = NOTICE_TONE[notice.severity];
  const Icon = tone.icon;
  return (
    <div
      role="status"
      data-slot="notice-banner"
      data-severity={notice.severity}
      className={cn(
        "flex items-start gap-3 border px-4 py-2.5 text-sm",
        tone.surface,
        className,
      )}
    >
      <Icon className={cn("mt-0.5 size-4 shrink-0", tone.ink)} aria-hidden />
      <p className="min-w-0 flex-1 whitespace-pre-line break-words text-foreground">
        <span className="sr-only">{tone.label}: </span>
        {notice.content}
        {notice.url && (
          <>
            {" "}
            <NoticeLink url={notice.url} />
          </>
        )}
      </p>
      {!notice.permanent && (
        <Button
          variant="ghost"
          size="icon-xs"
          className="-my-0.5 text-muted-foreground"
          aria-label="Dismiss notice"
          onClick={dismiss}
        >
          <XMarkIcon />
        </Button>
      )}
    </div>
  );
}

/**
 * A notice beside a list row: its severity's glyph, which reads the notice
 * in a tooltip and to a screen reader. Focusable, so the tooltip opens from
 * the keyboard too.
 */
export function NoticeIcon({ notice }: { notice: Notice }) {
  const tone = NOTICE_TONE[notice.severity];
  const Icon = tone.icon;
  return (
    <TooltipRoot>
      <TooltipTrigger asChild>
        <span
          role="img"
          tabIndex={0}
          aria-label={`${tone.label}: ${notice.content}`}
          data-slot="notice-icon"
          data-severity={notice.severity}
          className={cn(
            "inline-flex shrink-0 rounded-sm",
            tone.ink,
            FOCUS_RING,
          )}
        >
          <Icon className="size-4" aria-hidden />
        </span>
      </TooltipTrigger>
      <TooltipContent className="whitespace-pre-line">
        {notice.content}
      </TooltipContent>
    </TooltipRoot>
  );
}
