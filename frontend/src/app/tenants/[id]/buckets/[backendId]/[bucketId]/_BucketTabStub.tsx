"use client";

// Shared placeholder for bucket sub-tabs awaiting Phase 2+ wires: a title,
// a description and an optional legacy link, so operators get a consistent
// "this is intentionally a stub" signal.

import Link from "next/link";

import { Card, CardContent } from "@/components/ui/Card";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export function BucketTabStub({
  title,
  description,
  legacyHref,
  legacyLabel,
}: {
  title: string;
  description: string;
  legacyHref?: string;
  legacyLabel?: string;
}) {
  return (
    <Card>
      <CardContent className="space-y-2 p-6">
        <p className="text-sm font-medium">{title} — under refactor</p>
        <p className={cn(T.helper, "max-w-prose")}>{description}</p>
        {legacyHref && (
          <p>
            <Link
              href={legacyHref}
              className="text-sm font-medium text-primary hover:underline"
            >
              → {legacyLabel ?? legacyHref}
            </Link>
          </p>
        )}
      </CardContent>
    </Card>
  );
}
