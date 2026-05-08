import React from "react";
import Link from "next/link";
import { TagIcon } from "@heroicons/react/24/outline";
import { cn } from "@/lib/utils";

interface ObjectTagBadgeProps {
  objectTag: string;
  clickable?: boolean;
  linked?: boolean;
  onClick?: (objectTag: string) => void;
  className?: string;
}

export const ObjectTagBadge: React.FC<ObjectTagBadgeProps> = ({
  objectTag,
  clickable,
  linked = false,
  onClick,
  className,
}) => {
  const handleClick = () => {
    if (clickable && onClick) {
      onClick(objectTag);
    }
  };

  const isInteractive = clickable || linked;

  const badge = (
    <span
      onClick={handleClick}
      className={cn(
        "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-semibold border transition-all duration-200",
        "bg-indigo-500/10 border-indigo-500/20 text-indigo-300",
        isInteractive
          ? "cursor-pointer hover:bg-indigo-500/25 hover:border-indigo-400/40 hover:text-indigo-200 hover:-translate-y-px"
          : "cursor-default",
        className,
      )}
    >
      <TagIcon className="w-3 h-3 shrink-0" />
      {objectTag}
    </span>
  );

  if (linked) {
    return (
      <Link href={`/object-tags?search=${encodeURIComponent(objectTag)}`}>
        {badge}
      </Link>
    );
  }

  return badge;
};
