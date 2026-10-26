"use client";

import React from "react";
import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { capabilityOpGrants, procedureLabel } from "@/lib/capabilityOpGrants";
import { capabilityOperationGrantsQuery } from "@/lib/queries/capabilityOperationGrants";
import { STATUS_TONE } from "@/lib/ui/status";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** Joins a list where any one item will do: "get or put". */
const ANY_OF = "or";
/** Joins a list where every item holds: "Delegate and Revoke". */
const ALL_OF = "and";
/** Separates every item of a list but the last pair. */
const LIST_SEPARATOR = ", ";

/**
 * The names, each as code, as a sentence lists them: comma-separated, or
 * with the conjunction before the last when one is given.
 */
function OpList({
  ops,
  conjunction,
}: {
  ops: readonly string[];
  conjunction?: string;
}) {
  const last = ops.length - 1;
  return (
    <>
      {ops.map((op, i) => (
        <React.Fragment key={op}>
          {i > 0 &&
            (conjunction !== undefined && i === last
              ? ` ${conjunction} `
              : LIST_SEPARATOR)}
          <code className={T.codeSmall}>{op}</code>
        </React.Fragment>
      ))}
    </>
  );
}

/**
 * What a capability's ops unlock with no tenant policy, from the server's
 * table (CapabilityService.ListOperationGrants), by each op's basis: the
 * Cedar actions the built-in policy grants; a warning for the ops that
 * unlock nothing until a tenant policy permits them — a call under those is
 * otherwise denied by policy; the ops that only add a check beside others;
 * and the ops the caveats alone decide, with the RPCs they open. The read
 * never holds a form: while it loads or when it fails, this says so and
 * nothing else waits on it.
 */
export function CapabilityOpGrants({ ops }: { ops: readonly string[] }) {
  const query = useQuery(capabilityOperationGrantsQuery());
  if (ops.length === 0) return null;
  if (query.isPending) {
    return <p className={T.hint}>Reading what these ops unlock…</p>;
  }
  if (query.isError) {
    return <p className={T.hint}>What these ops unlock is unavailable.</p>;
  }

  const { granted, needsPolicy, paired, caveatsOnly, unknown } =
    capabilityOpGrants(ops, query.data);
  const Warning = STATUS_TONE.warning.icon;
  const single = (list: readonly string[]) => list.length === 1;
  return (
    <div role="group" className="space-y-1" aria-label="What these ops unlock">
      {granted.map((g) => (
        <div key={g.op} className="flex flex-wrap items-center gap-1">
          <code className={T.codeSmall}>{g.op}</code>
          <span className="text-xs text-muted-foreground">unlocks</span>
          {g.actions.map((a) => (
            <Badge key={a} variant="secondary" className={T.codeSmall}>
              {a}
            </Badge>
          ))}
        </div>
      ))}
      {needsPolicy.length > 0 && (
        <p
          role="note"
          className={cn(
            "flex items-start gap-1 text-xs",
            STATUS_TONE.warning.ink,
          )}
        >
          <Warning className="mt-px size-3.5 shrink-0" aria-hidden />
          <span>
            <OpList ops={needsPolicy} />{" "}
            {single(needsPolicy) ? "unlocks" : "unlock"} nothing on{" "}
            {single(needsPolicy) ? "its" : "their"} own: without a tenant policy
            that permits {single(needsPolicy) ? "its" : "their"} actions, each
            call is denied by policy.
          </span>
        </p>
      )}
      {paired.map((g) => (
        <p key={g.op} className={T.hint}>
          <code className={T.codeSmall}>{g.op}</code> adds a check beside{" "}
          <OpList ops={g.pairedOps} conjunction={ANY_OF} />; it unlocks nothing
          on its own.
        </p>
      ))}
      {caveatsOnly.map((g) => (
        <p key={g.op} className={T.hint}>
          <code className={T.codeSmall}>{g.op}</code> is decided by the caveats
          alone: it opens{" "}
          <OpList ops={g.procedures.map(procedureLabel)} conjunction={ALL_OF} />
          .
        </p>
      ))}
      {unknown.length > 0 && (
        <p className={T.hint}>
          The server&apos;s table does not say what <OpList ops={unknown} />{" "}
          {single(unknown) ? "unlocks" : "unlock"};{" "}
          {single(unknown) ? "it is" : "they are"} not counted as granted.
        </p>
      )}
    </div>
  );
}
