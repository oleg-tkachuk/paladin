import { MAX_LIST_PAGES } from "@/hooks/useBuckets";

/** One page of a list RPC: its rows, and the token for the next page. */
export interface Page<T> {
  items: T[];
  next: string;
}

/**
 * Every row of a paged list, following its page token to the end, up to
 * MAX_LIST_PAGES pages. A list that stops at the first page claims the rest
 * are not there, and a search over it misses them.
 */
export async function listAllPages<T>(
  page: (token: string) => Promise<Page<T>>,
): Promise<T[]> {
  const acc: T[] = [];
  let token = "";
  let pages = 0;
  do {
    const res = await page(token);
    acc.push(...res.items);
    token = res.next;
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return acc;
}
