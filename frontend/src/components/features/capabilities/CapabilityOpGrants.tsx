"use client";

import React from "react";
import { useQuery } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { capabilityOpGrants } from "@/lib/capabilityOpGrants";
import { capabilityOperationGrantsQuery } from "@/lib/queries/capabilityOperationGrants";
import { STATUS_TONE } from "@/lib/ui/status";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** The ops, each as code, separated as a sentence lists them. */
function OpList({ ops }: { ops: readonly string[] }) {
  return (
    <>
      {ops.map((op, i) => (
        <React.Fragment key={op}>
          {i > 0 && ", "}
          <code className={T.codeSmall}>{op}</code>
        </React.Fragment>
      ))}
    </>
  );
}

/**
 * What a capability's ops unlock with no tenant policy, from the server's
 * table (CapabilityService.ListOperationGrants): each op's Cedar actions,
 * then a warning for the ops that unlock nothing until a tenant policy
 * permits them — a call under those is otherwise denied by policy. The read
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

  const { granted, needsPolicy, unknown } = capabilityOpGrants(ops, query.data);
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
      {unknown.length > 0 && (
        <p className={T.hint}>
          The server lists no built-in grant for <OpList ops={unknown} />: a
          tenant policy decides what {single(unknown) ? "it" : "they"} may do.
        </p>
      )}
    </div>
  );
}
