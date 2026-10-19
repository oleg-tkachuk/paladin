import { CheckIcon } from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";

/**
 * What a success says beside the control that made it ("Saved"), and to a
 * screen reader through the live region it sits in. The region is always
 * there, so the words are announced when they appear.
 */
export function Confirmation({
  shown,
  children,
  className,
}: {
  shown: boolean;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      role="status"
      aria-live="polite"
      className={cn(
        "inline-flex items-center gap-1 text-sm text-success",
        className,
      )}
    >
      {shown && (
        <>
          <CheckIcon aria-hidden className="size-4" />
          {children}
        </>
      )}
    </span>
  );
}
