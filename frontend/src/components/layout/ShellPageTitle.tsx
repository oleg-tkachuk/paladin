"use client";

import { useCrumbNames } from "@/context/CrumbNamesContext";
import { useShell } from "@/context/ShellContext";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { titleWithActivity } from "@/lib/tabTitle";

import { pageTitle } from "./crumbs";

const NO_OPERATIONS: Operation[] = [];

/**
 * The title of a page in the shell, led by the user's operations: how many
 * run, and a mark while one has failed — what a background tab can show.
 */
export function ShellPageTitle({ pathname }: { pathname: string }) {
  const { operations } = useShell();
  const base = pageTitle(pathname, useCrumbNames());
  return (
    <title>{titleWithActivity(base, operations.data ?? NO_OPERATIONS)}</title>
  );
}
