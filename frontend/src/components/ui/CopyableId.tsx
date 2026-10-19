"use client";

// An identifier as it reads inline — mono, breaking where it must — with the
// one-click copy every identifier in the console carries (IdentifierCopy).
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { cn } from "@/lib/utils";

export function CopyableId({
  value,
  label,
  className,
}: {
  value: string;
  /** What the value is, for the button's name: "tenant ID". */
  label: string;
  className?: string;
}) {
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1", className)}>
      <span className="break-all font-mono text-xs">{value}</span>
      <IdentifierCopy value={value} label={label} iconOnly className="p-1" />
    </span>
  );
}
