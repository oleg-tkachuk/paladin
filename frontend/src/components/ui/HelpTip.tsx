"use client";

// A "?" beside a term, opening its short explanation and the document that
// decides it (lib/help). A popover, not a tooltip: the link has to be
// reachable, and on a phone there is no hover.
import { QuestionMarkCircleIcon } from "@heroicons/react/24/outline";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { HELP, helpHref, type HelpTopic } from "@/lib/help";

export function HelpTip({ topic }: { topic: HelpTopic }) {
  const entry = HELP[topic];
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`What is ${entry.term.toLowerCase()}?`}
          className="inline-flex items-center text-muted-foreground hover:text-foreground"
        >
          <QuestionMarkCircleIcon className="size-4" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80 space-y-2 text-sm">
        <p className="font-medium">{entry.term}</p>
        <p className="text-muted-foreground">{entry.text}</p>
        <a
          href={helpHref(topic)}
          target="_blank"
          rel="noreferrer"
          className="text-xs text-primary hover:underline"
        >
          Read {entry.docLabel}
        </a>
      </PopoverContent>
    </Popover>
  );
}
