"use client";

import { useEffect } from "react";

import { useCrumbNames } from "@/context/CrumbNamesContext";
import { useShell } from "@/context/ShellContext";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import {
  applyFavicon,
  badgedIconUrl,
  dotColor,
  originalIconHref,
} from "@/lib/favicon";
import { activitySignal, titleWithActivity } from "@/lib/tabTitle";

import { pageTitle } from "./crumbs";

const NO_OPERATIONS: Operation[] = [];

/**
 * The title of a page in the shell, led by the user's operations: how many
 * run, and a mark while one has failed — what a background tab can show.
 * The tab's icon carries the same signal as a dot.
 */
export function ShellPageTitle({ pathname }: { pathname: string }) {
  const { operations } = useShell();
  const ops = operations.data ?? NO_OPERATIONS;
  const base = pageTitle(pathname, useCrumbNames());
  const signal = activitySignal(ops);

  useEffect(() => {
    if (signal === "idle") {
      applyFavicon(null);
      return;
    }
    const src = originalIconHref();
    if (!src) return;
    let current = true;
    badgedIconUrl(src, dotColor(signal))
      .then((url) => {
        if (current && url) applyFavicon(url);
      })
      // An icon that cannot be drawn leaves the plain one; the title still
      // carries the signal.
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [signal]);

  useEffect(() => () => applyFavicon(null), []);

  return <title>{titleWithActivity(base, ops)}</title>;
}
