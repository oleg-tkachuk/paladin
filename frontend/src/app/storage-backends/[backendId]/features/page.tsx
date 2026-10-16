"use client";

import { useMemo } from "react";
import { useParams } from "next/navigation";

import { useBackends } from "@/hooks/useBackends";
import { Card, CardContent } from "@/components/ui/Card";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";

import { BackendFeatures } from "../BackendFeatures";
import { BackendHeader } from "../BackendHeader";

/** A backend's S3 feature probe, on a tab of its own. */
export default function BackendFeaturesPage() {
  const params = useParams<{ backendId: string }>();
  const backendId = decodeURIComponent(params.backendId);
  const { backends, loading, error, fetchBackends } = useBackends();
  const backend = useMemo(
    () => backends.find((b) => b.backendId === backendId),
    [backends, backendId],
  );

  return (
    <div className="space-y-6">
      <BackendHeader backendId={backendId} backend={backend} />
      {backend ? (
        <BackendFeatures backend={backend} />
      ) : loading ? (
        <Skeleton className="h-64 w-full" />
      ) : error ? (
        <Card>
          <CardContent className="py-8">
            <ListLoadError
              what="Backends"
              reason={error}
              onRetry={() => void fetchBackends()}
            />
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="py-8 text-center text-sm text-muted-foreground">
            Backend not found.
          </CardContent>
        </Card>
      )}
    </div>
  );
}
