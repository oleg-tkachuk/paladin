"use client";

// TabStub — temporary placeholder rendered by Phase-0/1 sub-tab
// routes that don't yet have real content. Each tab page stays a
// distinct file (so Phase-N replacing the body doesn't churn route
// boundaries), but they share this stub so the interim experience
// reads consistently.

import Link from "next/link";

import { Card, CardContent } from "@/components/ui/Card";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export function TabStub({
  title,
  legacyHref,
  legacyLabel,
  description,
}: {
  title: string;
  legacyHref?: string;
  legacyLabel?: string;
  description?: string;
}) {
  return (
    <Card>
      <CardContent className="space-y-2 p-6">
        <p className="text-sm font-medium">{title} — under refactor</p>
        {description && (
          <p className={cn(T.helper, "max-w-prose")}>{description}</p>
        )}
        {legacyHref && (
          <p>
            <Link
              href={legacyHref}
              className="text-sm font-medium text-primary hover:underline"
            >
              → Open {legacyLabel || legacyHref}
            </Link>
          </p>
        )}
      </CardContent>
    </Card>
  );
}
