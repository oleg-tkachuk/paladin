import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";

/** The label for a bucket the viewed tenant owns outright. */
export const OWNED_BUCKET_LABEL = "owned";
/** The label for a bucket the viewed tenant stores in alongside others. */
export const SHARED_BUCKET_LABEL = "shared";

/**
 * Whether a bucket on a tenant's list is the tenant's own (dedicated layout,
 * ADR-0015) or a shared one it stores in through its default binding or a
 * collection. A shared bucket is managed from the cross-tenant /buckets page.
 */
export function BucketTenancyBadge({ owned }: { owned: boolean }) {
  return owned ? (
    <Badge
      variant="info"
      className={T.code}
      title="Owned by this tenant alone."
    >
      {OWNED_BUCKET_LABEL}
    </Badge>
  ) : (
    <Badge
      variant="secondary"
      className={T.code}
      title="Shared with other tenants; this tenant stores in it through its default binding or a collection."
    >
      {SHARED_BUCKET_LABEL}
    </Badge>
  );
}
