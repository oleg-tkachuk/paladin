"use client";

// The dashboard's attention cards — failed operations, budget alerts, the
// user's operations, quotas — each say "nothing here" most of the time, and
// four cards of nothing pushed what does need attention below the fold. A
// card with nothing to report reports it here instead of rendering, and the
// dashboard names all of them on one line. Each keeps running its own read,
// so it is back the moment there is something to show.

import Link from "next/link";
import { CheckCircleIcon } from "@heroicons/react/24/outline";
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** A card that may have nothing to report. */
export interface QuietCard {
  /** Its place on the All clear line; the line lists cards in this order. */
  id: QuietCardId;
  /** What having nothing means, as the line says it. */
  label: string;
  /** Where its full view is. */
  href: string;
}

/** The cards in the order the All clear line names them. */
export const QUIET_CARD_ORDER = [
  "failedOperations",
  "budgetAlerts",
  "myOperations",
  "quotas",
] as const;
export type QuietCardId = (typeof QUIET_CARD_ORDER)[number];

/** What the line opens with. */
export const ALL_CLEAR = "All clear";

const QuietContext = createContext<{
  report: (card: QuietCard, quiet: boolean) => void;
} | null>(null);

/**
 * Collects the cards with nothing to report and names them on one line above
 * its children. Without it a card renders as it always did.
 */
export function QuietCards({ children }: { children: ReactNode }) {
  const [quiet, setQuiet] = useState<ReadonlyMap<QuietCardId, QuietCard>>(
    () => new Map(),
  );
  const value = useMemo(
    () => ({
      report: (card: QuietCard, isQuiet: boolean) =>
        setQuiet((prev) => {
          if (isQuiet === prev.has(card.id)) return prev;
          const next = new Map(prev);
          if (isQuiet) next.set(card.id, card);
          else next.delete(card.id);
          return next;
        }),
    }),
    [],
  );
  const cards = QUIET_CARD_ORDER.flatMap((id) => {
    const card = quiet.get(id);
    return card ? [card] : [];
  });
  return (
    <QuietContext.Provider value={value}>
      {cards.length > 0 && (
        <p
          role="status"
          className={cn(
            T.hint,
            "flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border bg-card/40 px-3 py-2",
          )}
        >
          <span className="inline-flex items-center gap-1.5 font-medium text-success">
            <CheckCircleIcon className="size-4" />
            {ALL_CLEAR}
          </span>
          {cards.map((c) => (
            <Link key={c.id} href={c.href} className="hover:text-foreground">
              {c.label}
            </Link>
          ))}
        </p>
      )}
      {children}
    </QuietContext.Provider>
  );
}

/**
 * Reports card as quiet while it has nothing to report, and says whether to
 * render nothing in its place: true only inside QuietCards, which names it
 * instead.
 */
export function useQuiet(card: QuietCard, quiet: boolean): boolean {
  const ctx = useContext(QuietContext);
  const { id, label, href } = card;
  useEffect(() => {
    if (!ctx) return;
    ctx.report({ id, label, href }, quiet);
    return () => ctx.report({ id, label, href }, false);
  }, [ctx, id, label, href, quiet]);
  return ctx !== null && quiet;
}
