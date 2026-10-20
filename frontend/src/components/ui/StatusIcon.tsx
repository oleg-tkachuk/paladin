import { STATUS_TONE, type StatusTone } from "@/lib/ui/status";
import { cn } from "@/lib/utils";

/**
 * A state as a glyph in its tone's colour: the shape says it as well as the
 * colour, so it reads without colour vision. `label` names it for a screen
 * reader and a tooltip; without one the icon is decoration and the text
 * beside it names the state.
 */
export function StatusIcon({
  tone,
  label,
  className,
}: {
  tone: StatusTone;
  label?: string;
  className?: string;
}) {
  const { icon: Icon, ink } = STATUS_TONE[tone];
  return (
    <Icon
      data-slot="status-icon"
      data-tone={tone}
      className={cn("size-4 shrink-0", ink, className)}
      // A heroicon hides itself unless told otherwise.
      {...(label
        ? {
            role: "img",
            "aria-label": label,
            "aria-hidden": undefined,
            title: label,
          }
        : { "aria-hidden": true })}
    />
  );
}

/**
 * A state as a dot, for where a glyph is too much (a dense row, the top
 * bar). Always beside the text that names the state.
 */
export function StatusDot({
  tone,
  className,
}: {
  tone: StatusTone;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      data-slot="status-dot"
      data-tone={tone}
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        STATUS_TONE[tone].fill,
        className,
      )}
    />
  );
}

/** A state named in words, with its glyph before it, in its tone's colour. */
export function StatusLabel({
  tone,
  children,
  className,
}: {
  tone: StatusTone;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      data-slot="status-label"
      className={cn(
        "inline-flex items-center gap-1.5 text-sm font-medium",
        STATUS_TONE[tone].ink,
        className,
      )}
    >
      <StatusIcon tone={tone} />
      {children}
    </span>
  );
}
