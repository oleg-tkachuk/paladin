import React from "react";
import { TagIcon } from "@heroicons/react/24/outline";
import { cn } from "@/lib/utils";

interface ObjectTagBadgeProps {
  objectTag: string;
  linked?: boolean;
  className?: string;
}

export const ObjectTagBadge: React.FC<ObjectTagBadgeProps> = ({
  objectTag,
  linked = false,
  className,
}) => {
  // No caller made the badge clickable; the click handler it carried could
  // never run, and a span is not reachable from the keyboard anyway.
  const isInteractive = linked;

  const badge = (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-semibold border transition-all duration-200",
        "bg-primary/10 border-primary/20 text-primary",
        isInteractive
          ? "cursor-pointer hover:bg-primary/25 hover:border-primary/40 hover:text-primary hover:-translate-y-px"
          : "cursor-default",
        className,
      )}
    >
      <TagIcon className="w-3 h-3 shrink-0" />
      {objectTag}
    </span>
  );

  // `linked` used to point at the legacy /object-tags taxonomy
  // page (deleted in Phase 5). Until the BACKLOG "Object Tags as a
  // filter on Objects tab" lands, the badge has nowhere meaningful
  // to navigate; render the same badge non-link, no behaviour change
  // for callers that pass `linked` (just no href).
  return badge;
};
