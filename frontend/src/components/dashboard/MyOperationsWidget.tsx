"use client";

// The signed-in user's own latest operations, as the shell already polls
// them (ShellContext) — no request of its own.
import Link from "next/link";
import { QueueListIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { useShell } from "@/context/ShellContext";
import { useQuiet, type QuietCard } from "@/components/dashboard/quiet";
import { SECTION } from "@/lib/navigation/sections";
import {
  OP_STATUS_BADGE,
  operationLabel,
  operationStatus,
} from "@/lib/operations";

/** How many of the user's operations the card lists. */
export const MY_OPERATIONS_SHOWN = 5;

const MY_OPERATIONS_CARD: QuietCard = {
  id: "myOperations",
  label: "Nothing of yours running",
  href: SECTION.operations,
};

export function MyOperationsWidget() {
  const { operations } = useShell();
  const ops = (operations.data ?? []).slice(0, MY_OPERATIONS_SHOWN);
  const quiet =
    operations.status !== "loading" &&
    operations.status !== "unavailable" &&
    ops.length === 0;
  if (useQuiet(MY_OPERATIONS_CARD, quiet)) return null;
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm" icon={QueueListIcon}>
          My operations
          <Link
            href={SECTION.operations}
            className="ml-auto text-xs font-normal text-muted-foreground hover:text-foreground"
          >
            All operations
          </Link>
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {operations.status === "loading" ? (
          <Skeleton className="h-12 w-full" />
        ) : operations.status === "unavailable" ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            Operations unavailable
            {operations.reason ? `: ${operations.reason}` : "."}
          </p>
        ) : ops.length === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            Nothing you started is running or recent.
          </p>
        ) : (
          <ul className="space-y-1.5">
            {ops.map((op) => {
              const badge = OP_STATUS_BADGE[operationStatus(op)];
              return (
                <li key={op.name} className="flex items-center gap-2 text-xs">
                  <span className="min-w-0 flex-1 truncate">
                    {operationLabel(op)}
                  </span>
                  <Badge variant={badge.variant}>{badge.label}</Badge>
                </li>
              );
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
