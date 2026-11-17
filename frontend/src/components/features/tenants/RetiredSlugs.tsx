"use client";

// The slugs purged tenants held. A purge records each one so that a Cedar
// policy, a token claim, an audit row or a client's tenants/<slug> path that
// still names it cannot come to mean a new tenant. While
// admin.tenants.reserve_slugs is on, a new tenant is refused them until one
// is released here, or, with admin.tenants.slug_reservation_period set,
// until the worker releases it that long after the purge.

import { useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { LockOpenIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/Card";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { CopyableId } from "@/components/ui/CopyableId";
import { useNotification } from "@/components/ui/Notification";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { errorMessage } from "@/hooks/errorContract";
import { useRetiredTenantSlugs } from "@/hooks/useRetiredTenantSlugs";
import { formatDate, formatDateTime } from "@/lib/format/locale";
import { retiredSlugReleaseDate } from "@/lib/tenant/retiredSlug";
import { RETIRED_SLUGS_ANCHOR } from "@/lib/tenant/slugHolder";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

export function RetiredSlugs() {
  const { slugs, enforced, loading, error, releaseSlug } =
    useRetiredTenantSlugs();
  const [pending, setPending] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { showNotification } = useNotification();

  const handleRelease = async (slug: string) => {
    setBusy(true);
    try {
      await releaseSlug(slug);
      showNotification({
        type: "success",
        title: "Slug released",
        message: `${slug} can be taken by a new tenant`,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Release failed",
        message: errorMessage(err),
      });
    } finally {
      setBusy(false);
      setPending(null);
    }
  };

  return (
    <section
      id={RETIRED_SLUGS_ANCHOR}
      aria-labelledby={`${RETIRED_SLUGS_ANCHOR}-title`}
      className="scroll-mt-20 space-y-3"
    >
      <div>
        <h2
          id={`${RETIRED_SLUGS_ANCHOR}-title`}
          className="text-base font-semibold"
        >
          Retired slugs
        </h2>
        <p className={T.helper}>
          {enforced
            ? "Slugs of purged tenants. A new tenant cannot take one until it is released."
            : "Slugs of purged tenants. Reservation is off (admin.tenants.reserve_slugs: false), so a new tenant may take any of them."}
        </p>
      </div>

      {error && (
        <Card>
          <CardContent className="py-6 text-center text-xs text-destructive">
            {error}
          </CardContent>
        </Card>
      )}

      {loading && <Skeleton className="h-12 w-full" />}

      {!loading && !error && slugs.length === 0 && (
        <Card>
          <CardContent className="py-6 text-center text-xs text-muted-foreground">
            No retired slugs. A purge records the slug of the tenant it removes.
          </CardContent>
        </Card>
      )}

      {slugs.length > 0 && (
        <Card className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Slug</TableHead>
                <TableHead>Former tenant</TableHead>
                <TableHead className="@md:w-40">Retired</TableHead>
                <TableHead className="@md:w-40">Releases</TableHead>
                <TableHead className="@md:w-32">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {slugs.map((r) => {
                const retired = r.retireTime
                  ? timestampDate(r.retireTime)
                  : undefined;
                const release = retiredSlugReleaseDate(r);
                return (
                  <TableRow key={r.slug}>
                    <TableCell>
                      <CopyableId value={r.slug} label="Slug" />
                    </TableCell>
                    <TableCell>
                      <CopyableId
                        value={r.formerTenantId}
                        label="Former tenant ID"
                        className="text-muted-foreground"
                      />
                    </TableCell>
                    <TableCell>
                      {retired ? (
                        <span
                          className="text-sm tabular-nums"
                          title={formatDateTime(retired)}
                        >
                          {formatDate(retired)}
                        </span>
                      ) : (
                        <span className={T.hint}>—</span>
                      )}
                    </TableCell>
                    <TableCell>
                      {release ? (
                        <span
                          className="text-sm tabular-nums"
                          title={formatDateTime(release)}
                        >
                          {formatDate(release)}
                        </span>
                      ) : (
                        <span className={T.hint}>Until released</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setPending(r.slug)}
                          disabled={busy}
                          aria-label={`Release ${r.slug}`}
                        >
                          <LockOpenIcon className="size-3.5" />
                          Release
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}

      <ConfirmModal
        isOpen={pending !== null}
        onClose={() => setPending(null)}
        onConfirm={() => (pending ? handleRelease(pending) : undefined)}
        loading={busy}
        type="danger"
        typeToConfirm={pending ?? undefined}
        title="Release this slug?"
        message={
          <p>
            <span className={cn(T.code, "text-foreground")}>{pending}</span>{" "}
            becomes free for a new tenant to take.
          </p>
        }
        warning="Whatever still names this slug — a Cedar policy, a token, an audit filter, a client's tenants/<slug> path — will name the new tenant instead. A release cannot be undone."
        confirmText="Release slug"
      />
    </section>
  );
}
