"use client";

// The bucket's one page (ADR-0033): its header, its tabs, and the bucket
// fetched once for all of them. It used to have a copy under its owner's
// workspace, which held the settings; that route now redirects here.

import { type Dispatch, type SetStateAction, useState } from "react";
import Link from "next/link";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { notFound, useParams } from "next/navigation";
import { ConnectError, Code } from "@connectrpc/connect";
import { ArrowPathIcon, MegaphoneIcon } from "@heroicons/react/24/outline";

import { BucketTabs } from "@/components/layout/BucketTabs";
import { PageHeader } from "@/components/layout/PageHeader";
import { NoticeBanner } from "@/components/ui/Notice";
import { noticeFromLabels } from "@/lib/notice";
import { Skeleton } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/button";
import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import { bucketDetailQuery, tenantDetailQuery } from "@/lib/queries/details";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";
import { tenantHref } from "@/lib/navigation/hrefs";
import { TRASHED_TENANT_BLOCK } from "@/app/tenants/[id]/tenant-context";
import { cn } from "@/lib/utils";

import { BucketProvider, type BucketOwner } from "./bucket-context";
import { SharedBucketNotice } from "./SharedBucketNotice";
import { BucketNoticeDialog } from "./BucketNoticeDialog";

export default function BucketLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const params = useParams<{ backendId: string; bucketId: string }>();
  const backendId = decodeURIComponent(params?.backendId ?? "");
  const bucketId = decodeURIComponent(params?.bucketId ?? "");

  const queryClient = useQueryClient();
  // The query a buckets row and a bucket hover card warm, so the page opens
  // on the bucket they already read.
  const bucketDetail = bucketDetailQuery(backendId, bucketId);
  const bucketKey = bucketDetail.queryKey;
  const bucketQuery = useQuery(bucketDetail);
  const bucket = bucketQuery.data ?? null;
  const loading = bucketQuery.isFetching;
  const notFoundFlag =
    bucketQuery.error instanceof ConnectError &&
    bucketQuery.error.code === Code.NotFound;
  const error =
    bucketQuery.error && !notFoundFlag
      ? errorMessage(bucketQuery.error, "Failed to load bucket.")
      : null;
  const refetch = async () => {
    await bucketQuery.refetch();
  };
  const setBucket: Dispatch<SetStateAction<Bucket | null>> = (next) =>
    queryClient.setQueryData<Bucket>(bucketKey, (curr) => {
      const resolved = typeof next === "function" ? next(curr ?? null) : next;
      return resolved ?? undefined;
    });

  // The owner names the bucket's header and decides whether it takes
  // changes: a bucket whose owner is in the trash is frozen with it.
  const ownerId = bucket?.ownerTenantId ?? "";
  const ownerQuery = useQuery({
    ...tenantDetailQuery(ownerId),
    enabled: ownerId !== "",
    select: (t): BucketOwner => ({
      tenantId: t.tenantId,
      slug: t.slug,
      displayName: t.displayName,
      trashed: t.deletedAt !== undefined,
    }),
  });
  const owner = ownerQuery.data ?? null;
  const changesBlocked = owner?.trashed ? TRASHED_TENANT_BLOCK : null;
  const ownerName = owner?.displayName || owner?.slug || ownerId;
  // The operator's word on this bucket, from its labels (notice.paladin.io/*).
  const notice = noticeFromLabels(bucket?.labels);
  const [noticeOpen, setNoticeOpen] = useState(false);

  if (notFoundFlag) {
    notFound();
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={bucketId}
        description={
          bucket?.displayName ? bucket.displayName : `Bucket on ${backendId}`
        }
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setNoticeOpen(true)}
              disabled={!bucket || changesBlocked !== null}
              title={changesBlocked ?? undefined}
            >
              <MegaphoneIcon className="size-4" />
              Edit notice
            </Button>
            <Button
              variant="outline"
              size="icon"
              onClick={() => void refetch()}
              aria-label="Refresh"
              disabled={loading}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
            </Button>
          </>
        }
      />

      {bucket && (
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <span>Owned by</span>
          <Link
            href={tenantHref(owner?.slug || ownerId)}
            title={ownerId}
            className="font-medium text-foreground hover:text-primary"
          >
            {ownerName}
          </Link>
          {bucket.publicRead && <PublicReadBadge />}
        </div>
      )}

      {notice && <NoticeBanner notice={notice} className="rounded-md" />}

      {bucket && noticeOpen && (
        <BucketNoticeDialog
          bucket={bucket}
          open={noticeOpen}
          onClose={() => setNoticeOpen(false)}
          onSaved={(saved) => setBucket(saved)}
        />
      )}

      <BucketTabs backendId={backendId} bucketId={bucketId} />

      {error && !bucket ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
          {error}
        </div>
      ) : loading && !bucket ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : bucket ? (
        <BucketProvider
          value={{ bucket, setBucket, refetch, owner, changesBlocked }}
        >
          <SharedBucketNotice />
          {children}
        </BucketProvider>
      ) : null}
    </div>
  );
}
