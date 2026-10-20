"use client";

import type React from "react";
import { useRef, useState } from "react";

import { CopyButton } from "@/components/ui/RecordDialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/**
 * Marks a row of a list whose details open in a DetailPanel, by the row's id
 * (useDetailPanel sets it): the panel hands focus back to it on closing, and
 * a click on another marked row moves the panel instead of closing it.
 */
export const DETAIL_ROW_ATTR = "data-detail-row";

export interface DetailTab {
  value: string;
  label: string;
  content: React.ReactNode;
}

/** The marked row with this id, when it is on the page and takes focus. */
function rowOf(id: string): HTMLElement | null {
  // Compared as attributes rather than built into a selector: an id is free
  // text, and quoting it into one is a way to get it wrong.
  const row = Array.from(
    document.querySelectorAll<HTMLElement>(`[${DETAIL_ROW_ATTR}]`),
  ).find((el) => el.getAttribute(DETAIL_ROW_ATTR) === id);
  return row && row.tabIndex >= 0 ? row : null;
}

/**
 * One record's details, in a panel that slides in from the right while the
 * list stays where it was, after Argo CD's sliding panel. Read beside the
 * list rather than over it: the panel does not dim the page or hold focus,
 * so the list can still be scrolled, and a click on another row shows that
 * row instead. Escape, the close button or a click elsewhere closes it, and
 * focus goes back to the row it was opened from.
 *
 * It is for reading a record. A form that changes one stays a dialog, which
 * holds focus until it is answered.
 */
export function DetailPanel({
  open,
  onClose,
  title,
  identifier,
  description,
  rowId,
  tabs,
  tab,
  onTabChange,
  footer,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  /** The record's identifier, in mono under the title, with a copy. */
  identifier?: string;
  /** One line under the title, for a record with no identifier to show. */
  description?: string;
  /** The row the panel was opened from, which takes focus back on close. */
  rowId?: string | null;
  /** Views of the record, each a tab; without them the panel has one body. */
  tabs?: DetailTab[];
  tab?: string;
  onTabChange?: (tab: string) => void;
  /** Actions on the record, along the bottom. */
  footer?: React.ReactNode;
  children?: React.ReactNode;
}) {
  // A click elsewhere on the page closes the panel and keeps focus where it
  // landed; only a close from the panel itself returns focus to the row.
  const closedByOutside = useRef(false);
  // The row to give focus back to. Held past the close, since closing is what
  // clears the open row the panel was given.
  const [lastRow, setLastRow] = useState(rowId);
  if (rowId && rowId !== lastRow) setLastRow(rowId);
  const shownTab = tabs?.some((t) => t.value === tab) ? tab : tabs?.[0]?.value;

  return (
    <Sheet
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      modal={false}
    >
      <SheetContent
        side="right"
        className="gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-xl data-[side=right]:lg:max-w-2xl"
        onOpenAutoFocus={() => {
          closedByOutside.current = false;
        }}
        onPointerDownOutside={(ev) => {
          const target = ev.target as Element | null;
          if (target?.closest?.(`[${DETAIL_ROW_ATTR}]`)) {
            ev.preventDefault();
            return;
          }
          closedByOutside.current = true;
        }}
        // Focus leaving the panel, into the list, does not close it: the
        // list is meant to be used beside it.
        onFocusOutside={(ev) => ev.preventDefault()}
        onCloseAutoFocus={(ev) => {
          if (closedByOutside.current || !lastRow) return;
          const row = rowOf(lastRow);
          if (!row) return;
          ev.preventDefault();
          row.focus();
        }}
      >
        <SheetHeader className="gap-1 border-b border-border px-5 pt-5 pb-3 pr-12">
          <SheetTitle>{title}</SheetTitle>
          {identifier ? (
            <div className="flex min-w-0 items-start gap-1">
              <SheetDescription
                className={cn(
                  T.codeSmall,
                  "min-w-0 break-all text-muted-foreground",
                )}
              >
                {identifier}
              </SheetDescription>
              <CopyButton
                value={identifier}
                label={`Copy ${title.toLowerCase()} id`}
              />
            </div>
          ) : (
            <SheetDescription className={description ? undefined : "sr-only"}>
              {description ?? title}
            </SheetDescription>
          )}
        </SheetHeader>
        {tabs && tabs.length > 0 ? (
          <Tabs
            value={shownTab}
            onValueChange={onTabChange}
            className="min-h-0 flex-1 gap-0"
          >
            <div className="border-b border-border px-5 py-2">
              <TabsList>
                {tabs.map((t) => (
                  <TabsTrigger key={t.value} value={t.value}>
                    {t.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>
            {tabs.map((t) => (
              <TabsContent
                key={t.value}
                value={t.value}
                className="min-h-0 overflow-y-auto px-5 py-4"
              >
                {t.content}
              </TabsContent>
            ))}
          </Tabs>
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
            {children}
          </div>
        )}
        {footer ? (
          <SheetFooter className="mt-0 flex-row flex-wrap justify-end border-t border-border px-5 py-3">
            {footer}
          </SheetFooter>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
