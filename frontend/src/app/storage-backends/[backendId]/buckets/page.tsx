import { redirect } from "next/navigation";

import { backendHref } from "@/lib/navigation/hrefs";

/**
 * A backend's buckets are listed on the backend's page. This route exists so
 * the bucket page's trail has no crumb that leads nowhere (ADR-0033).
 */
export default async function BackendBucketsPage({
  params,
}: {
  params: Promise<{ backendId: string }>;
}) {
  const { backendId } = await params;
  redirect(backendHref(decodeURIComponent(backendId)));
}
