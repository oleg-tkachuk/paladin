import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";

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
      public
    </Badge>
  );
}
