"use client";

import { useCallback, useMemo } from "react";

import { DETAIL_ROW_ATTR } from "@/components/ui/DetailPanel";
import { useLinkedRow } from "@/hooks/useLinkedRow";
import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import {
  PAGE_TABLE,
  choiceUpdate,
  parseChoice,
  tableParam,
  type TableUrl,
} from "@/lib/table/urlState";

/** No tabs: a panel that shows one body. */
const NO_TABS: readonly string[] = [];

function tabCodec<T extends string>(
  url: TableUrl,
  tabs: readonly T[],
): UrlCodec<T> | null {
  if (tabs.length === 0) return null;
  const key = tableParam("tab", url);
  const first = tabs[0];
  return {
    read: (params) => parseChoice(params.get(key), tabs, first),
    write: (tab) => choiceUpdate(tab, first, key),
  };
}

/**
 * A list's detail panel, after Argo CD's sliding panel: the row it shows and
 * the tab it is on, both in the URL (`?id=` and `?tab=`, under the table's
 * namespace), so an open panel is a link, survives a reload, and the back
 * button is not filled by moving between rows. The first tab stays out of the
 * URL. Every row is marked, so the panel can hand focus back to the row it
 * was opened from, and a click on another row moves the panel rather than
 * closing it.
 *
 * `tabs` and `url` are module-level constants, so the codecs keep their
 * identity between renders.
 */
export function useDetailPanel<T extends string = string>(
  tabs: readonly T[] = NO_TABS as readonly T[],
  url: TableUrl = PAGE_TABLE,
) {
  const linked = useLinkedRow(url);
  const codec = useMemo(() => tabCodec(url, tabs), [url, tabs]);
  const first = tabs[0] ?? ("" as T);
  const [tab, setTab] = useUrlState<T>(codec, first);

  const { open: openRow, close: closeRow, rowProps: linkedProps } = linked;
  const open = useCallback(
    (id: string, on: T = first) => {
      openRow(id);
      setTab(on);
    },
    [openRow, setTab, first],
  );
  const close = useCallback(() => {
    closeRow();
    setTab(first);
  }, [closeRow, setTab, first]);
  const rowProps = useCallback(
    (id: string) => ({
      ...linkedProps(id),
      [DETAIL_ROW_ATTR]: id,
    }),
    [linkedProps],
  );

  return {
    /** The row the panel shows; null while it is closed. */
    openId: linked.linkedId,
    tab,
    setTab,
    open,
    close,
    rowProps,
  };
}
