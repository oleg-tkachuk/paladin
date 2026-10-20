"use client";

// A bucket's history: the audit entries that name it or what it holds — its
// settings, its grants, its quota — newest first. What tenants store in it is
// their own history, under each tenant.

import { HistoryTimeline } from "@/components/features/history/HistoryTimeline";
import { useResourceHistory } from "@/hooks/useResourceHistory";
import { auditFilterFor } from "@/lib/history";

import { useBucket } from "../bucket-context";

export default function BucketHistoryPage() {
  const { bucket } = useBucket();
  const history = useResourceHistory({
    kind: "resource",
    filter: auditFilterFor(bucket.name),
  });
  return (
    <HistoryTimeline
      history={history}
      what="this bucket"
      description="Changes to the bucket, its settings, grants and quota, newest first."
    />
  );
}
