"use client";

// A notice on screen, across a page as a banner. Its severity takes the
// console's status tokens — info the info ink, warning the warning tone, critical the danger
// tone — with the tone's glyph, so it reads without colour as well.
import type React from "react";
import Link from "next/link";
import {
  ArrowTopRightOnSquareIcon,
  InformationCircleIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { useNoticeDismissal } from "@/hooks/useNoticeDismissal";
import type { Notice, NoticeSeverity } from "@/lib/notice";
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
