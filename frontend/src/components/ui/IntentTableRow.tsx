"use client";

import type React from "react";
import type { FetchQueryOptions, QueryKey } from "@tanstack/react-query";

import { TableRow } from "@/components/ui/table";
import { usePrefetchOnIntent } from "@/hooks/usePrefetchOnIntent";

/** A row that warms its detail page's query when pointed at or focused. */
export function IntentTableRow<
  TQueryFnData,
  TError,
  TData,
  TQueryKey extends QueryKey,
>({
  query,
  ...props
}: React.ComponentProps<typeof TableRow> & {
  query: FetchQueryOptions<TQueryFnData, TError, TData, TQueryKey>;
}) {
  const intent = usePrefetchOnIntent(query);
  return <TableRow {...props} {...intent} />;
}
