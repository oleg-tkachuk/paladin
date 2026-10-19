import Link from "next/link";

import { bucketHref, parseBucketName } from "@/lib/navigation/hrefs";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { BucketHoverCard } from "@/components/features/hover/ResourceHoverCards";

/**
 * A bucket in a list cell: its name, linking to its page, with the resource
 * name on hover. The resource name in full widened a table past the page.
 */
export function BucketNameLink({ bucket }: { bucket: string }) {
  const parts = parseBucketName(bucket);
  if (!parts) return <span className="text-muted-foreground">—</span>;
  return (
    <BucketHoverCard backendId={parts.backendId} bucketId={parts.bucketId}>
      <Link
        href={bucketHref(parts.backendId, parts.bucketId)}
        title={bucket}
        className={cn(
          T.codeSmall,
          "block max-w-56 truncate hover:text-primary",
        )}
      >
        {parts.bucketId}
      </Link>
    </BucketHoverCard>
  );
}
