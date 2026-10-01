"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { ChevronRightIcon, HomeIcon } from "@heroicons/react/20/solid";

import { cn } from "@/lib/utils";

import { crumbs } from "./crumbs";

/**
 * Breadcrumbs — the "you are here" trail, and the primary way to climb
 * back out of the deep tenant → bucket → surface hierarchy (a bucket's
 * identity is tenant + backend + name, so those URLs run five segments
 * deep by necessity). The trail is the anchor that keeps that depth
 * legible, so it reads as a sentence, not a code string: route names in
 * plain words, resource identifiers in mono, the current page in solid
 * foreground.
 *
 * The labels come from ./crumbs, which the document title reads too.
 */
export function Breadcrumbs() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const trail = crumbs(pathname);
  const paths = trail.map((c) => c.segment);

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
          const { label, mono } = trail[index];

          let href = `/${paths.slice(0, index + 1).join("/")}`;
          if (isLast && searchParams.toString()) {
            href += `?${searchParams.toString()}`;
          }

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
                    "max-w-55 truncate font-medium text-foreground",
                    mono && "font-mono text-compact",
                  )}
                >
                  {label}
                </span>
              ) : (
                <Link
                  href={href}
                  title={title}
                  className={cn(
                    "max-w-45 truncate text-muted-foreground transition-colors hover:text-foreground",
                    mono && "font-mono text-compact",
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
