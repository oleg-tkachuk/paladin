// A test router for pages that keep state in the URL: replace writes the query that
// useSearchParams reads back, and re-renders whoever reads it, as the app
// router does.
import { useSyncExternalStore } from "react";

let query = "";
let pathname = "/";
const listeners = new Set<() => void>();

export const urlRouter = {
  reset(path: string, search = "") {
    pathname = path;
    query = search;
    listeners.forEach((l) => l());
  },
  replace: (href: string) => {
    const [path, search = ""] = href.split("?");
    if (path) pathname = path;
    query = search;
    listeners.forEach((l) => l());
  },
  get search() {
    return query;
  },
  get pathname() {
    return pathname;
  },
};

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useTestSearchParams(): URLSearchParams {
  const q = useSyncExternalStore(subscribe, () => query);
  return new URLSearchParams(q);
}

export function useTestPathname(): string {
  return useSyncExternalStore(subscribe, () => pathname);
}
