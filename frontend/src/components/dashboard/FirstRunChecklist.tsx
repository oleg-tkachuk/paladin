"use client";

// The first steps of an empty installation, each a link to where it is done.
// Gone once every step is: an established platform does not need it.
import Link from "next/link";
import {
  CheckCircleIcon,
  QuestionMarkCircleIcon,
} from "@heroicons/react/24/outline";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { firstRunPending, firstRunSteps } from "@/lib/firstRun";
import { cn } from "@/lib/utils";

import { usePlatformSummary } from "./usePlatformSummary";

export function FirstRunChecklist() {
  const { data, error } = usePlatformSummary();
  // A failed read shows no checklist: the stats page reports it, and a
  // platform this one cannot count is not shown a first-run guide.
  if (error || !data) return null;
  const steps = firstRunSteps(data);
  if (!firstRunPending(steps)) return null;
  const done = steps.filter((s) => s.done).length;
  return (
    <Card data-testid="first-run">
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">
          Getting started · {done} of {steps.length}
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        <ol className="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
          {steps.map((s, i) => (
            <li key={s.id}>
              <Link
                href={s.href}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted",
                  s.done && "text-muted-foreground line-through",
                )}
              >
                {s.done === true ? (
                  <CheckCircleIcon className="size-4 text-success" />
                ) : s.done === undefined ? (
                  <QuestionMarkCircleIcon
                    className="size-4 text-muted-foreground"
                    aria-label="Unknown"
                  />
                ) : (
                  <span className="flex size-4 items-center justify-center rounded-full border text-xs">
                    {i + 1}
                  </span>
                )}
                {s.label}
              </Link>
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  );
}
