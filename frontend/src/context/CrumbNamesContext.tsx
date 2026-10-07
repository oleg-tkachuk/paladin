"use client";

/**
 * Names for the URL segments a path cannot name itself. A page whose path
 * carries ids — /users/<tenant-id>/<user-id> — registers what each id is
 * called, once it has read it, and the breadcrumb trail and the document
 * title show those names instead of the ids. A name may also say where its
 * crumb leads, for a segment whose own path is no page.
 */

import { createContext, useContext, useEffect, useMemo, useState } from "react";

export interface CrumbName {
  label: string;
  /** Where the crumb leads; the segment's own path when absent. */
  href?: string;
}

/** Names by URL segment, as the segment appears in the path. */
export type CrumbNames = Readonly<Record<string, CrumbName>>;

const NO_NAMES: CrumbNames = {};

interface CrumbNamesState {
  names: CrumbNames;
  setNames: (names: CrumbNames) => void;
}

const CrumbNamesCtx = createContext<CrumbNamesState | null>(null);

export function CrumbNamesProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [names, setNames] = useState<CrumbNames>(NO_NAMES);
  const value = useMemo(() => ({ names, setNames }), [names]);
  return (
    <CrumbNamesCtx.Provider value={value}>{children}</CrumbNamesCtx.Provider>
  );
}

/** The names the current page registered; none outside a provider. */
export function useCrumbNames(): CrumbNames {
  return useContext(CrumbNamesCtx)?.names ?? NO_NAMES;
}

/**
 * Registers `names` for as long as the calling page is mounted; null while
 * the page has not read them yet. A new object each render re-registers, so
 * pass a memoised one.
 */
export function useNameCrumbs(names: CrumbNames | null): void {
  const setNames = useContext(CrumbNamesCtx)?.setNames;
  useEffect(() => {
    if (!setNames || !names) return;
    setNames(names);
    return () => setNames(NO_NAMES);
  }, [setNames, names]);
}
