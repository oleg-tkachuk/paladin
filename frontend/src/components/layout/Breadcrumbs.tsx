"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { ChevronRightIcon, HomeIcon } from "@heroicons/react/20/solid";

import { cn } from "@/lib/utils";

/**
 * Breadcrumbs — the "you are here" trail, and the primary way to climb
 * back out of the deep tenant → bucket → surface hierarchy (a bucket's
 * identity is tenant + backend + name, so those URLs run five segments
 * deep by necessity). The trail is the anchor that keeps that depth
 * legible, so it reads as a sentence, not a code string: route names in
 * plain words, resource identifiers in mono, the current page in solid
 * foreground.
 *
 * ENTITY_PARENTS marks segments whose *child* is a resource id (slug /
 * key / UUID) rather than a route name — those children are decoded and
 * rendered in mono; everything else is title-cased.
 */
const ENTITY_PARENTS = new Set([
  "tenants",
  "buckets",
  "collections",
  "objects",
  "object-tags",
]);

function isEntityChild(parentSegment?: string): boolean {
  return !!parentSegment && ENTITY_PARENTS.has(parentSegment);
}

/** Resolve a URL segment into a readable label. */
function resolveLabel(segment: string, parentSegment?: string): string {
  if (isEntityChild(parentSegment)) {
    const decoded = decodeURIComponent(segment);
    // Keys can be paths ("folder/sub/file.txt") — show the leaf.
    const leaf = decoded.includes("/")
      ? decoded.split("/").pop() || decoded
      : decoded;
    return leaf.length > 32 ? leaf.slice(0, 29) + "…" : leaf;
  }
  return segment.charAt(0).toUpperCase() + segment.slice(1).replace(/-/g, " ");
}

export function Breadcrumbs() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const paths = pathname.split("/").filter(Boolean);

  if (paths.length === 0) return null;

  return (
    <nav aria-label="Breadcrumb" className="min-w-0">
      <ol className="paladin-scroll flex items-center gap-1.5 overflow-x-auto whitespace-nowrap text-sm">
        <li className="shrink-0">
          <Link
            href="/"
            className="flex items-center text-muted-foreground transition-colors hover:text-foreground"
          >
            <HomeIcon className="size-4" aria-hidden="true" />
            <span className="sr-only">Home</span>
          </Link>
        </li>
        {paths.map((path, index) => {
          const isLast = index === paths.length - 1;
          const parentSegment = index > 0 ? paths[index - 1] : undefined;
          const mono = isEntityChild(parentSegment);

          let href = `/${paths.slice(0, index + 1).join("/")}`;
          if (isLast && searchParams.toString()) {
            href += `?${searchParams.toString()}`;
          }

          const label = resolveLabel(path, parentSegment);
          const fullDecoded = decodeURIComponent(path);
          const title = fullDecoded !== label ? fullDecoded : undefined;

          return (
            <li
              key={`${path}-${index}`}
              className="flex min-w-0 items-center gap-1.5"
            >
              <ChevronRightIcon
                className="size-4 shrink-0 text-muted-foreground/40"
                aria-hidden="true"
              />
              {isLast ? (
                <span
                  title={title}
                  aria-current="page"
                  className={cn(
                    "max-w-[220px] truncate font-medium text-foreground",
                    mono && "font-mono text-[0.8125rem]",
                  )}
                >
                  {label}
                </span>
              ) : (
                <Link
                  href={href}
                  title={title}
                  className={cn(
                    "max-w-[180px] truncate text-muted-foreground transition-colors hover:text-foreground",
                    mono && "font-mono text-[0.8125rem]",
                  )}
                >
                  {label}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
