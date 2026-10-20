"use client";

import Link from "next/link";
import {
  ArrowRightIcon,
  ChevronDownIcon,
  ChevronUpIcon,
} from "@heroicons/react/24/outline";

import { HelpTip } from "@/components/ui/HelpTip";
import { Card, CardContent } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusLabel } from "@/components/ui/StatusIcon";
import { Button } from "@/components/ui/button";
import { usePreference } from "@/hooks/usePreference";
import type { HelpTopic } from "@/lib/help";
import { PREFERENCE_KEYS, readFlag } from "@/lib/preferences/keys";
import { FOCUS_RING } from "@/lib/ui/states";
import type { StatusTone } from "@/lib/ui/status";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** One fact a resource page leads with: what it is about and where it stands. */
export interface StatusCardSpec {
  /** What the card reports on: "Health", "Quota", "Last change". */
  title: string;
  tone: StatusTone;
  /** The state in a word or two: "Frozen", "Near limit". */
  value: string;
  /** One line of what the state means for this resource. */
  detail?: React.ReactNode;
  /** The term the card's "?" explains. */
  help?: HelpTopic;
  /** Where the reader goes for more. */
  href?: string;
  /** What that link says: "Open quotas". */
  linkLabel?: string;
  /** Still being read: the card holds a placeholder rather than a state. */
  pending?: boolean;
}

/**
 * One fact as a compact card: its title and help, its state as a glyph and a
 * word in its tone, a line of detail and a link to the page that has the rest.
 * After the cards of Argo CD's application status panel.
 */
export function StatusCard({
  title,
  tone,
  value,
  detail,
  help,
  href,
  linkLabel,
  pending,
}: StatusCardSpec) {
  return (
    <Card size="sm" data-slot="status-card" data-tone={tone}>
      <CardContent className="flex h-full flex-col gap-1.5">
        <div className="flex items-center justify-between gap-2">
          <p className={T.label}>{title}</p>
          {help && <HelpTip topic={help} />}
        </div>
        {pending ? (
          <Skeleton className="h-5 w-24" />
        ) : (
          <StatusLabel tone={tone} className="text-base">
            {value}
          </StatusLabel>
        )}
        {!pending && detail && (
          <p className={cn(T.hint, "line-clamp-2 break-words")}>{detail}</p>
        )}
        {href && (
          <Link
            href={href}
            className={cn(
              "mt-auto inline-flex items-center gap-1 self-start rounded-sm pt-1 text-xs text-primary hover:underline",
              FOCUS_RING,
            )}
          >
            {linkLabel ?? "Open"}
            <ArrowRightIcon aria-hidden className="size-3" />
          </Link>
        )}
      </CardContent>
    </Card>
  );
}

// As many cards to a row as the page has, up to four; the class names are
// spelled out so Tailwind finds them.
const GRID_COLUMNS: Record<number, string> = {
  1: "sm:grid-cols-1",
  2: "sm:grid-cols-2",
  3: "sm:grid-cols-2 lg:grid-cols-3",
  4: "sm:grid-cols-2 lg:grid-cols-4",
};
const WIDEST_GRID = "sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5";

/**
 * The facts a resource page leads with, as a row of status cards. Collapsed,
 * the row becomes one line — each fact's glyph and word — for a reader who
 * knows the page and wants the room. Whether it is collapsed is the user's
 * choice for every such panel, kept with their settings so it follows them
 * between browsers.
 */
export function StatusPanel({
  label,
  cards,
}: {
  /** What the panel is the status of, for a screen reader: "Tenant status". */
  label: string;
  cards: StatusCardSpec[];
}) {
  const [collapsed, setCollapsed] = usePreference(
    PREFERENCE_KEYS.collapseStatusPanels,
    readFlag,
  );
  const toggle = () => void setCollapsed(!collapsed).catch(() => undefined);

  if (collapsed) {
    return (
      <section aria-label={label} data-slot="status-panel" data-collapsed>
        <Card size="sm">
          <CardContent className="flex flex-wrap items-center gap-x-5 gap-y-2">
            {cards.map((c) => (
              <span
                key={c.title}
                className="inline-flex min-w-0 items-center gap-1.5 text-sm"
              >
                <span className="text-muted-foreground">{c.title}</span>
                {c.pending ? (
                  <Skeleton className="h-4 w-14" />
                ) : (
                  <StatusLabel tone={c.tone}>{c.value}</StatusLabel>
                )}
              </span>
            ))}
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto"
              aria-expanded={false}
              onClick={toggle}
            >
              <ChevronDownIcon aria-hidden className="size-4" />
              Show details
            </Button>
          </CardContent>
        </Card>
      </section>
    );
  }

  return (
    <section aria-label={label} data-slot="status-panel" className="space-y-2">
      <div
        className={cn(
          "grid grid-cols-1 gap-3",
          GRID_COLUMNS[cards.length] ?? WIDEST_GRID,
        )}
      >
        {cards.map((c) => (
          <StatusCard key={c.title} {...c} />
        ))}
      </div>
      <div className="flex justify-end">
        <Button
          variant="ghost"
          size="sm"
          aria-expanded
          onClick={toggle}
          className="text-muted-foreground"
        >
          <ChevronUpIcon aria-hidden className="size-4" />
          Collapse to one line
        </Button>
      </div>
    </section>
  );
}
