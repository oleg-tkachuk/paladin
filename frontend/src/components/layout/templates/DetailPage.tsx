"use client";

// The frame of a page about one resource: its header, then the resource —
// or, until it is read, a placeholder, and a failed read in its place.
import type React from "react";

import { Card } from "@/components/ui/Card";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";

import { PAGE_STACK, type ListError } from "./ListPage";

export function DetailPage({
  header,
  pending,
  error,
  children,
}: {
  header: React.ReactNode;
  /** The resource has not been read yet. */
  pending: boolean;
  /** The read failed: what the resource is, why, and a retry. */
  error: ListError | null;
  /** The resource, once read. */
  children: React.ReactNode;
}) {
  return (
    <div className={PAGE_STACK}>
      {header}
      {pending ? (
        <Skeleton className="h-40 w-full" />
      ) : error ? (
        <Card className="p-6">
          <ListLoadError {...error} />
        </Card>
      ) : (
        children
      )}
    </div>
  );
}
