import { redirect } from "next/navigation";

import { bucketHref } from "@/lib/navigation/hrefs";

/**
 * A bucket has one page, under its backend (ADR-0033). Its settings used to
 * be a copy under the owner's workspace; links into that copy, a tab
 * included, land on the same tab of the bucket's page.
 */
export default async function TenantBucketRedirect({
  params,
}: {
  params: Promise<{ backendId: string; bucketId: string; tab?: string[] }>;
}) {
  const { backendId, bucketId, tab = [] } = await params;
  redirect(
    bucketHref(
      decodeURIComponent(backendId),
      decodeURIComponent(bucketId),
      tab.map(decodeURIComponent).join("/"),
    ),
  );
}
