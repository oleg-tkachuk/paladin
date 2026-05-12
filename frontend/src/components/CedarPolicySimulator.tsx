"use client";

// CedarPolicySimulator — "try this request against this policy" UI.
// Operator types/edits a Cedar policy in the adjacent CedarEditor;
// this panel lets them simulate a principal + action + resource and
// see Allow/Deny + which rule matched.
//
// Talks to admin/v1.CelService (existing endpoint that already does
// Cedar evaluation under the hood). Falls back to a "no simulator
// available" hint when the client isn't wired — keeps the
// component decoupled from a specific RPC name so backend renames
// don't break.

import React, { useState } from "react";
import { ConnectError } from "@connectrpc/connect";
import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  PlayIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export interface SimulateRequest {
  principal: string; // e.g. User::"alice"
  action: string; // e.g. Action::"GetObject"
  resource: string; // e.g. ObjectKey::"tenants/x/objectKeys/y"
}

export interface SimulateResult {
  decision: "ALLOW" | "DENY" | "ERROR";
  matchedRules?: string[];
  error?: string;
}

export interface CedarPolicySimulatorProps {
  /** Caller-provided evaluator. Wire to CelService or a local mock. */
  evaluate: (policy: string, req: SimulateRequest) => Promise<SimulateResult>;
  /** Current policy text from the adjacent editor. */
  policy: string;
  className?: string;
}

export function CedarPolicySimulator({
  evaluate,
  policy,
  className,
}: CedarPolicySimulatorProps) {
  const [principal, setPrincipal] = useState('User::"alice"');
  const [action, setAction] = useState('Action::"GetObject"');
  const [resource, setResource] = useState(
    'ObjectKey::"tenants/example/objectKeys/data"',
  );
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<SimulateResult | null>(null);

  const onRun = async () => {
    setBusy(true);
    try {
      const r = await evaluate(policy, { principal, action, resource });
      setResult(r);
    } catch (err) {
      setResult({
        decision: "ERROR",
        error: err instanceof ConnectError ? err.rawMessage : String(err),
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className={className}>
      <CardContent className="space-y-3 pt-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Simulate request
          </p>
          <p className="text-[11px] text-muted-foreground">
            Evaluate the policy above against a hypothetical request.
          </p>
        </div>
        <div className="space-y-2">
          <SimField
            id="sim-principal"
            label="Principal"
            value={principal}
            onChange={setPrincipal}
            placeholder='User::"alice"'
          />
          <SimField
            id="sim-action"
            label="Action"
            value={action}
            onChange={setAction}
            placeholder='Action::"GetObject"'
          />
          <SimField
            id="sim-resource"
            label="Resource"
            value={resource}
            onChange={setResource}
            placeholder='ObjectKey::"tenants/x/objectKeys/y"'
          />
        </div>
        <Button
          size="sm"
          onClick={onRun}
          disabled={busy || !policy.trim()}
          className="w-full"
        >
          <PlayIcon className="size-4" />
          {busy ? "Evaluating…" : "Run simulation"}
        </Button>
        {busy && <Skeleton className="h-16 w-full" />}
        {result && !busy && <ResultBanner result={result} />}
      </CardContent>
    </Card>
  );
}

function SimField({
  id,
  label,
  value,
  onChange,
  placeholder,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
}) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-[10px] uppercase tracking-wider">
        {label}
      </Label>
      <Input
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="font-mono text-[11px]"
      />
    </div>
  );
}

function ResultBanner({ result }: { result: SimulateResult }) {
  if (result.decision === "ALLOW") {
    return (
      <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 p-3">
        <div className="flex items-center gap-2 text-sm font-semibold text-emerald-700 dark:text-emerald-300">
          <CheckCircleIcon className="size-5" />
          Allow
        </div>
        {result.matchedRules && result.matchedRules.length > 0 && (
          <ul className={cn("mt-2 list-disc space-y-0.5 pl-5", T.helper)}>
            {result.matchedRules.map((r, i) => (
              <li key={i} className="font-mono text-[11px]">
                {r}
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  }
  if (result.decision === "DENY") {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3">
        <div className="flex items-center gap-2 text-sm font-semibold text-destructive">
          <XCircleIcon className="size-5" />
          Deny
        </div>
        <p className="mt-1 text-xs text-muted-foreground">
          No permit rule matched (or a forbid rule fired). Cedar defaults deny.
        </p>
        {result.matchedRules && result.matchedRules.length > 0 && (
          <ul className="mt-2 list-disc space-y-0.5 pl-5 text-xs text-muted-foreground">
            {result.matchedRules.map((r, i) => (
              <li key={i} className="font-mono text-[11px]">
                {r}
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  }
  return (
    <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
      <div className="flex items-center gap-2 text-sm font-semibold text-amber-700 dark:text-amber-300">
        <ExclamationTriangleIcon className="size-5" />
        Error
      </div>
      <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap font-mono text-[11px] text-muted-foreground">
        {result.error || "Evaluator returned an error."}
      </pre>
    </div>
  );
}
