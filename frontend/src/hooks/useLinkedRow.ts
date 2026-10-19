"use client";

import { useCallback, useRef } from "react";

import { LINKED_ROW_ATTR } from "@/components/ui/table";
import { textParam, useUrlState } from "@/hooks/useUrlState";
import { PAGE_TABLE, tableParam, type TableUrl } from "@/lib/table/urlState";

// One codec per table namespace, so useUrlState sees the same object on
// every render.
const codecs = new Map<string, ReturnType<typeof textParam>>();
function rowCodec(url: TableUrl) {
  const key = tableParam("row", url);
  let codec = codecs.get(key);
  if (!codec) {
    codec = textParam(key);
    codecs.set(key, codec);
  }
  return codec;
}

/** What a linked row's element takes; nothing for any other row. */
export type LinkedRowProps =
  | Record<string, never>
  | {
      [LINKED_ROW_ATTR]: true;
      ref: (el: HTMLElement | null) => void;
    };

/**
 * The row of a list a link points at, kept in the URL as `?id=` (under the
 * table's namespace). The open record is that row, so an open record's
 * address is its link: opened from a link, the list opens the record,
 * highlights its row and scrolls to it.
 */
export function useLinkedRow(url: TableUrl = PAGE_TABLE) {
  const [linked, setLinked] = useUrlState(rowCodec(url), "");
  // The row already brought into view. A row opened on this page is on
  // screen, so it is marked as seen and the list does not jump to it.
  const shown = useRef<string | null>(null);

  const open = useCallback(
    (id: string) => {
      shown.current = id;
      setLinked(id);
    },
    [setLinked],
  );
  const close = useCallback(() => setLinked(""), [setLinked]);

  const rowProps = useCallback(
    (id: string): LinkedRowProps =>
      id !== linked || !id
        ? {}
        : {
            [LINKED_ROW_ATTR]: true,
            ref: (el) => {
              if (!el || shown.current === id) return;
              shown.current = id;
              el.scrollIntoView?.({ block: "center" });
            },
          },
    [linked],
  );

  return { linkedId: linked || null, open, close, rowProps };
}
