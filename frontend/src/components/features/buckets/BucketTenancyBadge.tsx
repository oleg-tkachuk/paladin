import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";

/** The label for a bucket the viewed tenant owns. */
export const OWNED_BUCKET_LABEL = "owned";
/** The label for a bucket another tenant owns and granted the viewed one. */
export const GRANTED_BUCKET_LABEL = "granted";

/**
 * Whether a bucket on a tenant's list is the tenant's own or one its owner
 * granted it (ADR-0032). Every bucket has an owner; a granted bucket's
 * settings and grants are managed from the bucket's page, under its owner.
 */
export function BucketTenancyBadge({ owned }: { owned: boolean }) {
  return owned ? (
    <Badge
      variant="info"
      className={T.code}
      title="Owned by this tenant; other tenants bind to it only if it grants them."
    >
      {OWNED_BUCKET_LABEL}
    </Badge>
  ) : (
    <Badge
      variant="secondary"
      className={T.code}
      title="Owned by another tenant, which granted this one the right to bind collections to it."
    >
      {GRANTED_BUCKET_LABEL}
    </Badge>
  );
}
