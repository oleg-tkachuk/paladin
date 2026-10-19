import { CHORD_KEY_SEPARATOR } from "@/lib/navShortcuts";
import { cn } from "@/lib/utils";

/**
 * A shortcut as its keys, one cap each: "G B" from the shortcut registry
 * (lib/navShortcuts), "⌘ K" from a page. Decorative: the action it sits on
 * carries the name.
 */
export function KeyChord({
  chord,
  className,
}: {
  chord: string;
  className?: string;
}) {
  return (
    <span aria-hidden className={cn("flex shrink-0 gap-1", className)}>
      {chord.split(CHORD_KEY_SEPARATOR).map((key, i) => (
        <kbd
          key={i}
          className="inline-flex h-5 min-w-5 items-center justify-center rounded border border-border bg-foreground/5 px-1 font-mono text-xs text-muted-foreground"
        >
          {key}
        </kbd>
      ))}
    </span>
  );
}
