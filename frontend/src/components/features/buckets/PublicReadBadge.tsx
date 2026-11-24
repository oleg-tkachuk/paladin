import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";

/** What the badge says: the bucket or collection is public. */
export const PUBLIC_READ_LABEL = "public";

/**
 * Marks a bucket or collection anyone may read by URL (ADR-0027) — on every
 * list and detail page that shows one, so public data is never mistaken for
 * private.
 */
export function PublicReadBadge() {
  return (
    <Badge
      variant="warning"
      className={T.code}
      title="Anyone with an object's URL reads it, unsigned."
    >
      {PUBLIC_READ_LABEL}
    </Badge>
  );
}
