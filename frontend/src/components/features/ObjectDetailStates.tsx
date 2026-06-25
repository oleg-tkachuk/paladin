import {
  ArrowLeftIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/Skeleton";

// Loading + not-found shells for the object detail view, extracted from
// ObjectDetailView so the main component holds only the resolved-object path.

export function ObjectDetailSkeleton() {
  return (
    <div
      className="space-y-6"
      role="status"
      aria-busy="true"
      aria-label="Loading object details"
    >
      <PageHeader
        title={
          <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight">
            <Skeleton className="size-8" />
            <Skeleton className="h-6 w-64" />
          </h1>
        }
        showDefaultActions={false}
      />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Skeleton className="h-[360px] w-full" />
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
        <Skeleton className="h-64 w-full" />
      </div>
    </div>
  );
}

export function ObjectNotFound({ onBack }: { onBack: () => void }) {
  return (
    <div className="space-y-6">
      <PageHeader
        title={
          <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight">
            <Button
              variant="ghost"
              size="icon"
              onClick={onBack}
              aria-label="Back to Objects"
            >
              <ArrowLeftIcon className="size-5" />
            </Button>
            <span>Object Not Found</span>
          </h1>
        }
        showDefaultActions={false}
      />
      <Card className="flex flex-col items-center gap-3 p-12 text-center">
        <ExclamationTriangleIcon className="size-10 text-destructive" />
        <div className="text-sm font-medium">Missing Object</div>
        <div className="max-w-md text-sm text-muted-foreground">
          The object you&apos;re looking for doesn&apos;t exist or you
          don&apos;t have access.
        </div>
        <Button variant="outline" size="sm" onClick={onBack}>
          Back to Objects
        </Button>
      </Card>
    </div>
  );
}
