"use client";

// Test-suite simulator for /policies.
//
// Replaces the single-form SimulatePanel with a list of named scenarios
// the operator can run together. Each row is a SimulateAuthz call —
// "as auditor, can I read?" / "as auditor, can I delete?" / "as agent,
// can I upload?" — with an optional `expected` outcome the suite checks
// against the actual decision. The summary line on top shows how many
// of the expectational cases passed, so you can iterate a Cedar policy
// in the Editor tab and see the suite go green without leaving the
// page.
//
// Persistence:
//   localStorage key: paladin:policies:testSuite:<tenantId>
//   Same tenant-scoped pattern as paladin:capabilities:lastBrowse so two
//   tenants on the same browser profile don't share suites. Schema is
//   the bare TestCase[] array — no version field, intentionally; if we
//   ever change the shape we'll bump to a wrapper { v, cases }.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  CheckCircleIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ExclamationTriangleIcon,
  PlayIcon,
  PlusIcon,
  TrashIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";
import { z } from "zod";

import { safeParseJson } from "@/lib/parseJson";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { useScope } from "@/context/ScopeContext";
import { policyClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";

// ─── types ────────────────────────────────────────────────────────────────

export interface TestCase {
  id: string;
  name: string;
  principalSubject: string;
  principalTenantId: string;
  rolesText: string; // comma-separated; matches the original SimulatePanel UX
  action: string;
  resourceName: string;
  expected: "allow" | "deny" | "any";
}

// Runtime schema for the localStorage-persisted test suite. Validated on read
// (safeParseJson) so a hand-edited / corrupt entry yields an empty suite
// instead of feeding malformed cases into the simulate loop.
const TestCaseSchema = z.object({
  id: z.string(),
  name: z.string(),
  principalSubject: z.string(),
  principalTenantId: z.string(),
  rolesText: z.string(),
  action: z.string(),
  resourceName: z.string(),
  expected: z.enum(["allow", "deny", "any"]),
});

interface CaseResult {
  allowed: boolean;
  matchedPolicies: string[];
  explanation: string;
  ranAt: number;
}

const STORAGE_PREFIX = "paladin:policies:testSuite";

// DEFAULT_ACTION seeds a new case. It has to be an action the Cedar schema
// declares (backend/policies/schema.cedarschema): the server refuses any other
// name, so a seed that drifts from the schema makes every fresh case fail.
export const DEFAULT_ACTION = "GetObject";

function newId(): string {
  return `tc_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

function defaultCase(seed?: TestCase, defaultResource?: string): TestCase {
  return {
    id: newId(),
    name: seed ? `${seed.name} (copy)` : "Untitled case",
    principalSubject: seed?.principalSubject ?? "",
    principalTenantId: seed?.principalTenantId ?? "",
    rolesText: seed?.rolesText ?? "platform.admin",
    action: seed?.action ?? DEFAULT_ACTION,
    resourceName: seed?.resourceName ?? defaultResource ?? "",
    expected: seed?.expected ?? "any",
  };
}

function caseStatus(
  c: TestCase,
  r: CaseResult | undefined,
): "unrun" | "pass" | "fail" | "unexpected" {
  if (!r) return "unrun";
  if (c.expected === "any") return r.allowed ? "pass" : "fail";
  const expectedAllow = c.expected === "allow";
  return expectedAllow === r.allowed ? "pass" : "unexpected";
}

// ─── component ────────────────────────────────────────────────────────────

export function TestSuite({
  defaultResource,
}: {
  defaultResource: string;
}): React.ReactElement {
  const { tenantId } = useScope();
  const { showNotification } = useNotification();

  const storageKey = `${STORAGE_PREFIX}:${tenantId || "_"}`;
  const [cases, setCases] = useState<TestCase[]>([]);
  const [results, setResults] = useState<Map<string, CaseResult>>(new Map());
  const [running, setRunning] = useState<Set<string>>(new Set());
  const [runningAll, setRunningAll] = useState(false);
  const [openIds, setOpenIds] = useState<Set<string>>(new Set());
  const hydratedRef = useRef(false);

  // Hydrate from localStorage once tenantId is known. Tenant-scoped
  // key — see header comment. setState is wrapped in a microtask so
  // we don't trigger react-hooks/set-state-in-effect: the lint rule
  // rejects synchronous setState inside an effect body, but allows
  // async transitions (matches the CEL hook's setTimeout pattern).
  useEffect(() => {
    if (!tenantId || hydratedRef.current) return;
    hydratedRef.current = true;
    let parsed: TestCase[] | null = null;
    try {
      const raw = window.localStorage.getItem(storageKey);
      const decoded = safeParseJson(z.array(TestCaseSchema), raw);
      if (decoded && decoded.length > 0) {
        parsed = decoded;
      }
    } catch {
      // private mode / quota — fall through to empty suite
    }
    if (parsed) {
      const restored = parsed;
      void Promise.resolve().then(() => {
        setCases(restored);
        // Default-open the first case so the suite isn't a blank
        // wall of headers on first paint.
        setOpenIds(new Set([restored[0].id]));
      });
    }
  }, [tenantId, storageKey]);

  // Persist whenever the suite changes post-hydration.
  useEffect(() => {
    if (!hydratedRef.current) return;
    try {
      window.localStorage.setItem(storageKey, JSON.stringify(cases));
    } catch {
      // best-effort
    }
  }, [cases, storageKey]);

  const toggleOpen = useCallback((id: string) => {
    setOpenIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const updateCase = useCallback((id: string, patch: Partial<TestCase>) => {
    setCases((prev) => prev.map((c) => (c.id === id ? { ...c, ...patch } : c)));
  }, []);

  const removeCase = useCallback((id: string) => {
    setCases((prev) => prev.filter((c) => c.id !== id));
    setResults((prev) => {
      const next = new Map(prev);
      next.delete(id);
      return next;
    });
    setOpenIds((prev) => {
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);

  const addCase = useCallback(() => {
    setCases((prev) => {
      const seed = prev[prev.length - 1];
      const tc = defaultCase(seed, defaultResource);
      // Open the new case so the user can immediately edit it.
      setOpenIds((open) => new Set(open).add(tc.id));
      return [...prev, tc];
    });
  }, [defaultResource]);

  const runOne = useCallback(
    async (tc: TestCase): Promise<CaseResult | null> => {
      try {
        const res = await policyClient.simulateAuthz({
          principalSubject: tc.principalSubject,
          principalTenantId: tc.principalTenantId,
          principalRoles: tc.rolesText
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean),
          action: tc.action,
          resourceName: tc.resourceName,
        });
        return {
          allowed: res.allowed,
          matchedPolicies: res.matchedPolicies,
          explanation: res.explanation,
          ranAt: Date.now(),
        };
      } catch (err) {
        const msg = errorMessage(err, "Simulate failed");
        showNotification({
          type: "error",
          title: `"${tc.name}" failed`,
          message: msg,
        });
        return null;
      }
    },
    [showNotification],
  );

  const runCase = useCallback(
    async (id: string) => {
      const tc = cases.find((c) => c.id === id);
      if (!tc) return;
      setRunning((prev) => new Set(prev).add(id));
      const result = await runOne(tc);
      setRunning((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      if (result) {
        setResults((prev) => {
          const next = new Map(prev);
          next.set(id, result);
          return next;
        });
      }
    },
    [cases, runOne],
  );

  const runAll = useCallback(async () => {
    if (cases.length === 0) return;
    setRunningAll(true);
    setRunning(new Set(cases.map((c) => c.id)));
    const entries = await Promise.all(
      cases.map(async (tc) => {
        const r = await runOne(tc);
        return [tc.id, r] as const;
      }),
    );
    setResults((prev) => {
      const next = new Map(prev);
      for (const [id, r] of entries) {
        if (r) next.set(id, r);
      }
      return next;
    });
    setRunning(new Set());
    setRunningAll(false);
  }, [cases, runOne]);

  const resetSuite = useCallback(() => {
    setCases([]);
    setResults(new Map());
    setOpenIds(new Set());
    try {
      window.localStorage.removeItem(storageKey);
    } catch {
      // best-effort
    }
  }, [storageKey]);

  // Suite-level summary: count cases that have an explicit expectation
  // and report pass / unexpected. "any"-expected cases don't contribute
  // to the pass/fail count — they only show ✓/✗ on themselves.
  const summary = useMemo(() => {
    let total = 0;
    let passed = 0;
    let unexpected = 0;
    for (const c of cases) {
      const r = results.get(c.id);
      if (!r || c.expected === "any") continue;
      total += 1;
      const status = caseStatus(c, r);
      if (status === "pass") passed += 1;
      else if (status === "unexpected") unexpected += 1;
    }
    return { total, passed, unexpected };
  }, [cases, results]);

  return (
    <div className="space-y-3">
      <Card className="flex flex-wrap items-center justify-between gap-3 p-3">
        <div className="space-y-0.5">
          <p className="text-sm font-medium">Test suite</p>
          <p className="text-xs text-muted-foreground">
            {cases.length === 0 ? (
              "Add cases to simulate authz against the live policy stack."
            ) : summary.total === 0 ? (
              <>
                {cases.length} case{cases.length === 1 ? "" : "s"} — no
                expectations set
              </>
            ) : (
              <>
                {summary.passed}/{summary.total} passed
                {summary.unexpected > 0 && (
                  <span className="text-destructive">
                    {" "}
                    ({summary.unexpected} unexpected)
                  </span>
                )}
              </>
            )}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={addCase}>
            <PlusIcon className="size-4" />
            Add case
          </Button>
          <Button
            size="sm"
            onClick={() => void runAll()}
            disabled={cases.length === 0 || runningAll}
          >
            <PlayIcon className="size-4" />
            {runningAll ? "Running…" : "Run all"}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={resetSuite}
            disabled={cases.length === 0}
          >
            Reset suite
          </Button>
        </div>
      </Card>

      {cases.length === 0 ? (
        <Card className="p-6 text-center text-sm text-muted-foreground">
          No cases yet. Click <em>Add case</em> to draft a scenario.
        </Card>
      ) : (
        <div className="space-y-2">
          {cases.map((tc) => (
            <CaseRow
              key={tc.id}
              tc={tc}
              result={results.get(tc.id)}
              isOpen={openIds.has(tc.id)}
              isRunning={running.has(tc.id)}
              onToggle={() => toggleOpen(tc.id)}
              onUpdate={(patch) => updateCase(tc.id, patch)}
              onRun={() => void runCase(tc.id)}
              onRemove={() => removeCase(tc.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── one case ─────────────────────────────────────────────────────────────

function CaseRow({
  tc,
  result,
  isOpen,
  isRunning,
  onToggle,
  onUpdate,
  onRun,
  onRemove,
}: {
  tc: TestCase;
  result: CaseResult | undefined;
  isOpen: boolean;
  isRunning: boolean;
  onToggle: () => void;
  onUpdate: (patch: Partial<TestCase>) => void;
  onRun: () => void;
  onRemove: () => void;
}): React.ReactElement {
  const status = caseStatus(tc, result);
  return (
    <Card className="overflow-hidden">
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-muted/40"
      >
        {isOpen ? (
          <ChevronDownIcon className="size-4 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
        )}
        <span className="flex-1 truncate text-sm font-medium">{tc.name}</span>
        <StatusPill status={status} isRunning={isRunning} />
        {tc.expected !== "any" && (
          <Badge variant="outline" className="text-sm uppercase">
            expects {tc.expected}
          </Badge>
        )}
      </button>

      {isOpen && (
        <div className="space-y-3 border-t bg-muted/20 p-3">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label="Case name">
              <Input
                value={tc.name}
                onChange={(e) => onUpdate({ name: e.target.value })}
                placeholder="auditor reads object"
              />
            </Field>
            <Field label="Expected outcome">
              <SelectRoot
                value={tc.expected}
                onValueChange={(v) =>
                  onUpdate({ expected: v as TestCase["expected"] })
                }
              >
                <SelectTrigger aria-label="Expected outcome" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="any">any</SelectItem>
                  <SelectItem value="allow">allow</SelectItem>
                  <SelectItem value="deny">deny</SelectItem>
                </SelectContent>
              </SelectRoot>
            </Field>
            <Field label="Principal subject">
              <Input
                value={tc.principalSubject}
                onChange={(e) => onUpdate({ principalSubject: e.target.value })}
                placeholder="user-12345"
              />
            </Field>
            <Field label="Principal tenant">
              <Input
                value={tc.principalTenantId}
                onChange={(e) =>
                  onUpdate({ principalTenantId: e.target.value })
                }
                placeholder="019dfeaa-94c3-…"
                className="font-mono text-xs"
              />
            </Field>
            <Field label="Roles (comma-separated)">
              <Input
                value={tc.rolesText}
                onChange={(e) => onUpdate({ rolesText: e.target.value })}
                placeholder="platform.admin, viewer"
              />
            </Field>
            <Field label="Action">
              <Input
                value={tc.action}
                onChange={(e) => onUpdate({ action: e.target.value })}
                placeholder={DEFAULT_ACTION}
                className="font-mono text-xs"
              />
            </Field>
            <div className="md:col-span-2">
              <Field label="Resource name">
                <Input
                  value={tc.resourceName}
                  onChange={(e) => onUpdate({ resourceName: e.target.value })}
                  placeholder="tenants/{id}/collections/{key}"
                  className="font-mono text-xs"
                />
              </Field>
            </div>
          </div>

          <div className="flex items-center justify-between gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={onRemove}
              className="text-destructive hover:text-destructive"
            >
              <TrashIcon className="size-4" />
              Remove
            </Button>
            <Button
              size="sm"
              onClick={onRun}
              disabled={isRunning || !tc.action || !tc.resourceName}
            >
              <PlayIcon className="size-4" />
              {isRunning ? "Running…" : "Re-run"}
            </Button>
          </div>

          {result && <ResultBlock result={result} />}
        </div>
      )}
    </Card>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs">{label}</Label>
      {children}
    </div>
  );
}

function StatusPill({
  status,
  isRunning,
}: {
  status: "unrun" | "pass" | "fail" | "unexpected";
  isRunning: boolean;
}): React.ReactElement {
  if (isRunning) {
    return (
      <Badge variant="outline" className="text-sm uppercase">
        running
      </Badge>
    );
  }
  if (status === "unrun") {
    return (
      <Badge variant="outline" className="text-sm uppercase">
        not run
      </Badge>
    );
  }
  if (status === "pass") {
    return (
      <span className={cn(T.pill, "text-chart-2 text-xs")}>
        <CheckCircleIcon className="size-3.5" /> allow
      </span>
    );
  }
  if (status === "fail") {
    return (
      <span className={cn(T.pill, "text-destructive text-xs")}>
        <XCircleIcon className="size-3.5" /> deny
      </span>
    );
  }
  return (
    <span className={cn(T.pill, "text-destructive text-xs")}>
      <ExclamationTriangleIcon className="size-3.5" /> unexpected
    </span>
  );
}

function ResultBlock({ result }: { result: CaseResult }): React.ReactElement {
  return (
    <div
      className={cn(
        "rounded-md border p-3 text-sm",
        result.allowed
          ? "border-success/40 bg-success/10 text-success"
          : "border-destructive/40 bg-destructive/10 text-destructive",
      )}
    >
      <div className="flex items-center gap-2 font-medium">
        {result.allowed ? (
          <CheckCircleIcon className="size-4" />
        ) : (
          <XCircleIcon className="size-4" />
        )}
        {result.allowed ? "ALLOWED" : "DENIED"}
      </div>
      {result.explanation && (
        <p className="mt-1 text-xs opacity-90">{result.explanation}</p>
      )}
      {result.matchedPolicies.length > 0 && (
        <div className="mt-2 space-y-1">
          <p className="text-xs font-medium">Matched policies</p>
          <ul className="list-inside list-disc text-xs opacity-90">
            {result.matchedPolicies.map((p, i) => (
              <li key={i} className="font-mono">
                {p}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

export type { CaseResult };
