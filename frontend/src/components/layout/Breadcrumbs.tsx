"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { ChevronRightIcon, HomeIcon } from "@heroicons/react/20/solid";

/**
 * Known parent routes that have detail pages with dynamic segments.
 * The breadcrumb labels the segment that follows by decoding it as a
 * resource identifier (slug / key / UUID) instead of a route name.
 *
 * Tenant subtree adds: tenants/<id>/buckets/<backend>/<name>/...
 * and tenants/<id>/object-keys/<key>/objects/<id>. Listing the
 * intermediate "buckets" / "object-keys" / "objects" parents keeps
 * the immediately-following segment treated as a resource id.
 */
const ENTITY_PARENTS = new Set([
  "tenants",
  "buckets",
  "object-keys",
  "objects",
  "object-tags",
]);

/**
 * Resolves a URL segment into a readable breadcrumb label.
 * For entity detail pages, decodes the key and shows a truncated version.
 */
function resolveLabel(segment: string, parentSegment?: string): string {
  // If this segment is a child of an entity parent, decode it as a key/name
  if (parentSegment && ENTITY_PARENTS.has(parentSegment)) {
    const decoded = decodeURIComponent(segment);
    // Show last path segment for keys like "folder/subfolder/file.txt"
    const shortName = decoded.includes("/")
      ? decoded.split("/").pop() || decoded
      : decoded;
    // Truncate if too long
    return shortName.length > 30 ? shortName.slice(0, 27) + "…" : shortName;
  }

  // Default: capitalize and replace dashes
  return segment.charAt(0).toUpperCase() + segment.slice(1).replace(/-/g, " ");
}

export function Breadcrumbs() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const paths = pathname.split("/").filter(Boolean);

  if (paths.length === 0) return null;

  return (
    <nav className="flex mb-6" aria-label="Breadcrumb">
      <ol className="flex items-center space-x-2">
        <li>
          <div>
            <Link
              href="/"
              className="text-muted-foreground hover:text-primary transition-colors"
            >
              <HomeIcon className="h-4 w-4 shrink-0" aria-hidden="true" />
              <span className="sr-only">Home</span>
            </Link>
          </div>
        </li>
        {paths.map((path, index) => {
          const isLast = index === paths.length - 1;
          const parentSegment = index > 0 ? paths[index - 1] : undefined;

          // Build href — preserve query params for the last segment (detail pages)
          let href = `/${paths.slice(0, index + 1).join("/")}`;
          if (isLast && searchParams.toString()) {
            href += `?${searchParams.toString()}`;
          }

          const label = resolveLabel(path, parentSegment);
          const fullDecoded = decodeURIComponent(path);

          return (
            <li key={`${path}-${index}`}>
              <div className="flex items-center">
                <ChevronRightIcon
                  className="h-4 w-4 flex-shrink-0 text-muted-foreground/50"
                  aria-hidden="true"
                />
                <Link
                  href={href}
                  title={fullDecoded !== label ? fullDecoded : undefined}
                  className={`ml-2 text-xs font-medium tracking-wide transition-colors max-w-[180px] truncate ${
                    isLast
                      ? "text-primary cursor-default pointer-events-none font-mono"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                  aria-current={isLast ? "page" : undefined}
                >
                  {label}
                </Link>
              </div>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
