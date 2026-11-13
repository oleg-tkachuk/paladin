"use client";

import { usePathname } from "next/navigation";

/**
 * A route's dynamic segments, read from the path rather than from the router.
 *
 * The console ships as a static export (ADR-0049): each dynamic segment is
 * built once, under a placeholder, and `useParams()` returns that placeholder
 * on every page served from it. The path in the address bar is the real one,
 * so the params come from there.
 *
 * A pattern is the route's directory under `src/app`, from the root:
 * `/tenants/[id]/collections/[name]`. It matches the path it names and every
 * path below it, so a layout and its pages read the same pattern.
 */

const SEGMENT_SEPARATOR = "/";
const DYNAMIC_SEGMENT = /^\[([A-Za-z]\w*)\]$/;

type ParamNames<P extends string> =
  P extends `${string}[${infer Name}]${infer Rest}`
    ? Name | ParamNames<Rest>
    : never;

/** The params a pattern names, each decoded; "" when the path is not under it. */
export type RouteParams<P extends string> = { [K in ParamNames<P>]: string };

function segments(path: string): string[] {
  return path.split(SEGMENT_SEPARATOR).filter((s) => s !== "");
}

/** A segment as it reads, or as it came when it is not valid percent-encoding. */
function decodeSegment(raw: string): string {
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

/**
 * The params of `pattern` in `pathname`, or null when the path is not the
 * pattern's or below it. A path segment keeps its own encoding until it is
 * decoded here, so an encoded `/` inside one (`a%2Fb`) stays inside it.
 */
export function matchRoute<P extends string>(
  pattern: P,
  pathname: string,
): RouteParams<P> | null {
  const want = segments(pattern);
  const have = segments(pathname);
  if (have.length < want.length) return null;
  const params: Record<string, string> = {};
  for (const [i, seg] of want.entries()) {
    const dynamic = DYNAMIC_SEGMENT.exec(seg);
    if (dynamic) params[dynamic[1]] = decodeSegment(have[i]);
    else if (seg !== have[i]) return null;
  }
  return params as RouteParams<P>;
}

/** Every param of `pattern` as an empty string: what a path outside it reads. */
function emptyParams<P extends string>(pattern: P): RouteParams<P> {
  const params: Record<string, string> = {};
  for (const seg of segments(pattern)) {
    const dynamic = DYNAMIC_SEGMENT.exec(seg);
    if (dynamic) params[dynamic[1]] = "";
  }
  return params as RouteParams<P>;
}

/** The current path's params under `pattern`, decoded. */
export function useRouteParams<P extends string>(pattern: P): RouteParams<P> {
  const pathname = usePathname() ?? "";
  return matchRoute(pattern, pathname) ?? emptyParams(pattern);
}
