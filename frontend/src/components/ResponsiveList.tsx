"use client";

// ResponsiveList — switches between a desktop <Table> and a stacked
// card list on narrow viewports. Most platform tables (tenants,
// buckets, OKs, audit) become unreadable on iPhone-width screens
// because they assume horizontal real estate. This wrapper lets a
// page declare both shapes once.
//
// Usage:
//   <ResponsiveList
//     items={tenants}
//     getKey={(t) => t.tenantId}
//     table={<TableHeader>...</TableHeader>}
//     row={(t) => <TableRow>...</TableRow>}
//     card={(t) => <Card>...</Card>}
//   />
//
// Breakpoint is `md` (≥ 768px) — below it we render the card list,
// at and above the table. The breakpoint is fixed for simplicity;
// pages with unusually wide columns can override by passing a
// different `breakpoint` ("sm" | "md" | "lg").

import React from "react";

import { Table, TableBody } from "@/components/ui/table";
import { cn } from "@/lib/utils";

export interface ResponsiveListProps<T> {
  items: T[];
  getKey: (item: T) => string;
  /** Header (TableHeader + TableRow + TableHead cells). */
  tableHeader: React.ReactNode;
  /** Render-prop for each desktop table row. */
  row: (item: T, index: number) => React.ReactNode;
  /** Render-prop for each mobile card. */
  card: (item: T, index: number) => React.ReactNode;
  /** Optional empty-state node (visible on both viewports). */
  empty?: React.ReactNode;
  /** Breakpoint at which we switch to table. Defaults `md`. */
  breakpoint?: "sm" | "md" | "lg";
  className?: string;
}

const HIDE_BELOW: Record<"sm" | "md" | "lg", string> = {
  sm: "hidden sm:block",
  md: "hidden md:block",
  lg: "hidden lg:block",
};

const SHOW_BELOW: Record<"sm" | "md" | "lg", string> = {
  sm: "sm:hidden",
  md: "md:hidden",
  lg: "lg:hidden",
};

export function ResponsiveList<T>({
  items,
  getKey,
  tableHeader,
  row,
  card,
  empty,
  breakpoint = "md",
  className,
}: ResponsiveListProps<T>) {
  if (items.length === 0 && empty) {
    return <div className={className}>{empty}</div>;
  }
  return (
    <>
      {/* Desktop table */}
      <div className={cn(HIDE_BELOW[breakpoint], className)}>
        <Table>
          {tableHeader}
          <TableBody>
            {items.map((item, idx) => (
              <React.Fragment key={getKey(item)}>
                {row(item, idx)}
              </React.Fragment>
            ))}
          </TableBody>
        </Table>
      </div>
      {/* Mobile cards */}
      <div className={cn(SHOW_BELOW[breakpoint], "space-y-2", className)}>
        {items.map((item, idx) => (
          <React.Fragment key={getKey(item)}>{card(item, idx)}</React.Fragment>
        ))}
      </div>
    </>
  );
}
