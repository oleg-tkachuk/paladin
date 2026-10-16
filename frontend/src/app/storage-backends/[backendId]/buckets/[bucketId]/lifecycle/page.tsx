"use client";

// Lifecycle rules CRUD for a single bucket — tenant-scoped path.
//
// The parent BucketDetailLayout already fetched the Bucket and
// owns the page header. This page renders only the rules list,
// editor dialog, and delete confirmation; mutation results go
// back into BucketContext so sibling tabs see the updated rule
// count without a refetch.
//
// Backend semantics:
//   - SetLifecycleRules is a destructive replace. Edit/Toggle/Delete
//     all build the full desired list locally and ship it.
//   - Optimistic concurrency via resourceVersion — on Aborted we
//     refetch (via context.refetch) and ask the operator to retry.

import React, { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";
import type { Duration } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ClockIcon,
  EllipsisHorizontalIcon,
  PencilIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";

import { bucketClient } from "@/lib/connect/client";
import {
  type LifecycleRule,
  LifecycleRuleSchema,
  LifecycleExpirationSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { useCELValidation } from "@/hooks/useCELValidation";
import { CELIndicator } from "@/components/ui/CELIndicator";

import { useBucket } from "../bucket-context";
import { errorMessage } from "@/hooks/errorContract";

// ─── duration helpers ────────────────────────────────────────────────────────
//
// google.protobuf.Duration in JS is `{ seconds: bigint, nanos: number }`.
// We never need sub-second precision for lifecycle, so we pin nanos to 0
// and round on read.

type DurationUnit = "h" | "d" | "m";

const SEC_PER_UNIT: Record<DurationUnit, bigint> = {
  h: 3600n,
  d: 86_400n,
  m: 2_592_000n, // 30d nominal — backend uses absolute Duration semantics
};

function buildDuration(amount: number, unit: DurationUnit): Duration {
  const safe = Number.isFinite(amount) && amount > 0 ? Math.floor(amount) : 0;
  return create(DurationSchema, {
    seconds: BigInt(safe) * SEC_PER_UNIT[unit],
    nanos: 0,
  });
}

function decomposeDuration(d?: Duration): {
  amount: number;
  unit: DurationUnit;
} {
  if (!d) return { amount: 0, unit: "d" };
  const sec = d.seconds;
  if (sec === 0n) return { amount: 0, unit: "d" };
  if (sec % SEC_PER_UNIT.m === 0n) {
    return { amount: Number(sec / SEC_PER_UNIT.m), unit: "m" };
  }
  if (sec % SEC_PER_UNIT.d === 0n) {
    return { amount: Number(sec / SEC_PER_UNIT.d), unit: "d" };
  }
  if (sec % SEC_PER_UNIT.h === 0n) {
    return { amount: Number(sec / SEC_PER_UNIT.h), unit: "h" };
  }
  return { amount: Number(sec / SEC_PER_UNIT.d), unit: "d" };
}

function formatDuration(d?: Duration): string {
  if (!d || d.seconds === 0n) return "0";
  const { amount, unit } = decomposeDuration(d);
  return `${amount}${unit}`;
}

const UNIT_LABEL: Record<DurationUnit, string> = {
  h: "hours",
  d: "days",
  m: "30-day months",
};

// ─── form types ──────────────────────────────────────────────────────────────

// A rule's action is an expiration. The server refuses transitions: no
// supported backend has storage classes to move objects to, so one was
// stored and never applied (BACKLOG). A bucket may still hold one from
// before, which the page shows as not applied and lets the operator delete.
interface RuleFormState {
  id: string;
  enabled: boolean;
  match: string;
  afterAmount: string;
  afterUnit: DurationUnit;
}

const DEFAULT_FORM: RuleFormState = {
  id: "",
  enabled: true,
  match: "",
  afterAmount: "30",
  afterUnit: "d",
};

const RULE_ID_REGEX = /^[a-z][a-z0-9-]{0,62}$/;

interface FormErrors {
  id?: string;
  match?: string;
  after?: string;
}

function validateForm(form: RuleFormState): FormErrors {
  const errors: FormErrors = {};
  if (!form.id.trim()) {
    errors.id = "Required.";
  } else if (!RULE_ID_REGEX.test(form.id.trim())) {
    errors.id =
      "Lowercase letters, digits, and dashes; must start with a letter.";
  }
  if (!form.match.trim()) {
    errors.match = "CEL expression cannot be empty.";
  }
  const amount = Number(form.afterAmount);
  if (!Number.isFinite(amount) || amount <= 0) {
    errors.after = "Must be a positive integer.";
  }
  return errors;
}

/** A stored transition: never applied, and refused by the server. */
function isTransition(rule: LifecycleRule): boolean {
  return rule.action.case === "transition";
}

function ruleToForm(rule: LifecycleRule): RuleFormState {
  if (rule.action.case === "expiration") {
    const { amount, unit } = decomposeDuration(rule.action.value.after);
    return {
      id: rule.id,
      enabled: rule.enabled,
      match: rule.match,
      afterAmount: String(amount),
      afterUnit: unit,
    };
  }
  return {
    ...DEFAULT_FORM,
    id: rule.id,
    enabled: rule.enabled,
    match: rule.match,
  };
}

function formToRule(form: RuleFormState): LifecycleRule {
  const after = buildDuration(Number(form.afterAmount), form.afterUnit);
  return create(LifecycleRuleSchema, {
    id: form.id.trim(),
    enabled: form.enabled,
    match: form.match.trim(),
    action: {
      case: "expiration",
      value: create(LifecycleExpirationSchema, { after }),
    },
  });
}

// ─── page ────────────────────────────────────────────────────────────────────

export default function BucketLifecyclePage() {
  const { bucket, setBucket, refetch, changesBlocked } = useBucket();
  const { showNotification } = useNotification();

  const [saving, setSaving] = useState(false);

  const [editorOpen, setEditorOpen] = useState(false);
  const [editorMode, setEditorMode] = useState<"create" | "edit">("create");
  const [form, setForm] = useState<RuleFormState>(DEFAULT_FORM);
  const [errors, setErrors] = useState<FormErrors>({});

  const [deleteTarget, setDeleteTarget] = useState<LifecycleRule | null>(null);

  const rules = bucket.lifecycleRules ?? [];
  const hasTransitions = rules.some(isTransition);

  const persistRules = useCallback(
    async (
      nextRules: LifecycleRule[],
      successMessage: string,
    ): Promise<boolean> => {
      setSaving(true);
      try {
        const updated = await bucketClient.setLifecycleRules({
          name: bucket.name,
          resourceVersion: bucket.resourceVersion,
          rules: nextRules,
        });
        setBucket(updated);
        showNotification({ type: "success", title: successMessage });
        return true;
      } catch (err) {
        if (err instanceof ConnectError && err.code === Code.Aborted) {
          await refetch();
          showNotification({
            type: "error",
            title: "Bucket changed elsewhere",
            message: "Reloaded the latest version — please retry your change.",
          });
          return false;
        }
        const msg = errorMessage(err, "Failed to save lifecycle rules.");
        showNotification({ type: "error", title: "Save failed", message: msg });
        return false;
      } finally {
        setSaving(false);
      }
    },
    [bucket.name, bucket.resourceVersion, refetch, setBucket, showNotification],
  );

  const openCreate = () => {
    setEditorMode("create");
    setForm(DEFAULT_FORM);
    setErrors({});
    setEditorOpen(true);
  };

  const openEdit = (rule: LifecycleRule) => {
    if (isTransition(rule)) return;
    setEditorMode("edit");
    setForm(ruleToForm(rule));
    setErrors({});
    setEditorOpen(true);
  };

  const handleSave = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const validation = validateForm(form);
    setErrors(validation);
    if (Object.keys(validation).length > 0) return;
    if (editorMode === "create" && rules.some((r) => r.id === form.id.trim())) {
      setErrors({ id: "A rule with this ID already exists." });
      return;
    }
    const next = formToRule(form);
    const merged = mergeRule(rules, next);
    const ok = await persistRules(
      merged,
      editorMode === "create" ? "Rule saved" : "Rule updated",
    );
    if (ok) setEditorOpen(false);
  };

  const handleToggle = async (rule: LifecycleRule) => {
    // Optimistic — flip in-context, persist, revert on failure.
    const previous = rules;
    const optimistic = rules.map((r) =>
      r.id === rule.id
        ? create(LifecycleRuleSchema, { ...r, enabled: !r.enabled })
        : r,
    );
    setBucket((b) => (b ? { ...b, lifecycleRules: optimistic } : b));
    const ok = await persistRules(
      optimistic,
      rule.enabled ? "Rule disabled" : "Rule enabled",
    );
    if (!ok) {
      setBucket((b) => (b ? { ...b, lifecycleRules: previous } : b));
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    const next = rules.filter((r) => r.id !== deleteTarget.id);
    const ok = await persistRules(next, "Rule deleted");
    if (ok) setDeleteTarget(null);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold">Lifecycle rules</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Expire objects by age and a CEL filter.
          </p>
        </div>
        <Button
          size="sm"
          onClick={openCreate}
          disabled={Boolean(changesBlocked)}
          title={changesBlocked ?? undefined}
        >
          <PlusIcon className="size-4" />
          Add rule
        </Button>
      </div>

      {rules.length === 0 ? (
        <Card className="flex flex-col items-center gap-3 p-10 text-center">
          <ClockIcon className="size-10 text-muted-foreground" />
          <h2 className={T.cardTitleProse}>No lifecycle rules</h2>
          <p className={cn(T.helper, "max-w-md")}>
            Lifecycle rules expire objects by age and a CEL filter. Background
            workers evaluate rules on the configured cadence.
          </p>
          <Button
            size="sm"
            onClick={openCreate}
            disabled={Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
          >
            <PlusIcon className="size-4" />
            Add rule
          </Button>
        </Card>
      ) : (
        <div className="space-y-3">
          {hasTransitions && (
            <div
              role="alert"
              className="rounded-md border border-warning/40 bg-warning/10 px-4 py-3 text-sm"
            >
              This bucket holds transition rules. They were never applied, and
              no change to its lifecycle rules saves while one is stored: delete
              them first.
            </div>
          )}
          {rules.map((rule) => (
            <RuleCard
              key={rule.id}
              rule={rule}
              busy={saving}
              onEdit={() => openEdit(rule)}
              onToggle={() => handleToggle(rule)}
              onDelete={() => setDeleteTarget(rule)}
            />
          ))}
        </div>
      )}

      <RuleEditor
        open={editorOpen}
        mode={editorMode}
        form={form}
        errors={errors}
        saving={saving}
        onChange={setForm}
        onCancel={() => setEditorOpen(false)}
        onSubmit={handleSave}
      />

      <ConfirmModal
        isOpen={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        type="danger"
        title="Delete this rule?"
        message={
          <p>
            Removes rule{" "}
            <span className="font-mono text-foreground">
              {deleteTarget?.id}
            </span>{" "}
            from this bucket&apos;s lifecycle.
          </p>
        }
        warning="Objects it already expired stay expired; only future evaluations skip it. To bring it back, add the rule again."
        confirmText="Delete rule"
      />
    </div>
  );
}

// ─── helpers ─────────────────────────────────────────────────────────────────

function mergeRule(
  rules: LifecycleRule[],
  next: LifecycleRule,
): LifecycleRule[] {
  const idx = rules.findIndex((r) => r.id === next.id);
  if (idx === -1) return [...rules, next];
  const copy = [...rules];
  copy[idx] = next;
  return copy;
}

function describeAction(rule: LifecycleRule): string {
  if (rule.action.case === "transition") {
    return `transition to ${rule.action.value.storageClass || "?"} after ${formatDuration(rule.action.value.after)} — not applied`;
  }
  if (rule.action.case === "expiration") {
    return `expire after ${formatDuration(rule.action.value.after)}`;
  }
  return "no action";
}

// ─── rule card ───────────────────────────────────────────────────────────────

interface RuleCardProps {
  rule: LifecycleRule;
  busy: boolean;
  onEdit: () => void;
  onToggle: () => void;
  onDelete: () => void;
}

function RuleCard({ rule, busy, onEdit, onToggle, onDelete }: RuleCardProps) {
  // A transition can only be deleted: editing or enabling it saves a rule
  // the server refuses.
  const deleteOnly = isTransition(rule);
  return (
    <Card
      className="cursor-pointer p-4 transition-colors hover:border-foreground/20"
      onClick={onEdit}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onEdit();
        }
      }}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1 space-y-1.5">
          <div className="flex items-center gap-2">
            <span
              className={cn(
                T.pillDot,
                rule.enabled ? "bg-chart-2" : "bg-muted-foreground/40",
              )}
              aria-hidden
            />
            <span className="font-mono text-sm font-medium text-foreground">
              {rule.id}
            </span>
            {deleteOnly ? (
              <Badge variant="warning" className={T.labelTight}>
                not applied
              </Badge>
            ) : (
              <Badge
                variant={rule.enabled ? "success" : "outline"}
                className={T.labelTight}
              >
                {rule.enabled ? "enabled" : "disabled"}
              </Badge>
            )}
          </div>
          <div className={cn(T.hint, "flex flex-wrap items-baseline gap-x-2")}>
            <span className="text-muted-foreground">match:</span>
            <code className="font-mono text-xs text-foreground/80 break-all">
              {rule.match}
            </code>
          </div>
          <div className={cn(T.hint, "flex flex-wrap items-baseline gap-x-2")}>
            <span className="text-muted-foreground">action:</span>
            <span className="text-foreground/80">{describeAction(rule)}</span>
          </div>
        </div>
        <div onClick={(e) => e.stopPropagation()}>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                aria-label={`Actions for rule ${rule.id}`}
              >
                <EllipsisHorizontalIcon className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={onEdit} disabled={busy || deleteOnly}>
                <PencilIcon className="size-4" />
                Edit
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={onToggle}
                disabled={busy || deleteOnly}
              >
                {rule.enabled ? "Disable" : "Enable"}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onSelect={onDelete}
                disabled={busy}
              >
                <TrashIcon className="size-4" />
                Delete
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </Card>
  );
}

// ─── editor dialog ───────────────────────────────────────────────────────────

interface RuleEditorProps {
  open: boolean;
  mode: "create" | "edit";
  form: RuleFormState;
  errors: FormErrors;
  saving: boolean;
  onChange: (f: RuleFormState) => void;
  onCancel: () => void;
  onSubmit: (e?: React.FormEvent) => void;
}

function RuleEditor({
  open,
  mode,
  form,
  errors,
  saving,
  onChange,
  onCancel,
  onSubmit,
}: RuleEditorProps) {
  const update = <K extends keyof RuleFormState>(
    key: K,
    value: RuleFormState[K],
  ) => onChange({ ...form, [key]: value });

  const celState = useCELValidation(form.match, "Object");
  const celBlocksSubmit =
    celState.status === "invalid" || celState.status === "validating";

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <DialogContent className="sm:max-w-140">
        <form onSubmit={onSubmit}>
          <DialogHeader>
            <DialogTitle>
              {mode === "create" ? "New lifecycle rule" : "Edit lifecycle rule"}
            </DialogTitle>
            <DialogDescription>
              {mode === "create"
                ? "Filter objects with a CEL expression and choose how long after creation they expire."
                : "Tune the match expression or the age. The rule ID cannot be changed."}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="grid grid-cols-[1fr_auto] items-end gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="rule-id">Rule ID</Label>
                <Input
                  id="rule-id"
                  autoFocus={mode === "create"}
                  className="font-mono text-xs"
                  placeholder="archive-stale-tmp"
                  value={form.id}
                  disabled={mode === "edit"}
                  onChange={(e) => update("id", e.target.value)}
                  aria-invalid={!!errors.id || undefined}
                />
                <p className={cn(T.hint, errors.id && "text-destructive")}>
                  {errors.id ??
                    "Lowercase, dashes allowed; must start with a letter."}
                </p>
              </div>
              <div className="flex flex-col items-end gap-1.5 pb-7">
                <Label
                  htmlFor="rule-enabled"
                  className="text-xs text-muted-foreground"
                >
                  Enabled
                </Label>
                <Switch
                  id="rule-enabled"
                  checked={form.enabled}
                  onCheckedChange={(v) => update("enabled", v)}
                />
              </div>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="rule-match">Match (CEL)</Label>
              <Textarea
                id="rule-match"
                className="font-mono text-xs min-h-20"
                // The placeholder is a worked example, so it has to be a
                // valid expression: identifiers are bare (the Object schema
                // declares size_bytes, not object.size_bytes) and CEL has no
                // digit separators. The previous value was wrong on both
                // counts, so an operator who copied it got two errors.
                placeholder="size_bytes > 1000000"
                value={form.match}
                onChange={(e) => update("match", e.target.value)}
                aria-invalid={!!errors.match || undefined}
              />
              <p className={cn(T.hint, errors.match && "text-destructive")}>
                {errors.match ??
                  "Evaluated against object fields. See the CEL reference for the available identifiers."}
              </p>
              <CELIndicator state={celState} />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="rule-after">After</Label>
              <div className="flex gap-2">
                <Input
                  id="rule-after"
                  type="number"
                  min={1}
                  className="font-mono text-xs flex-1"
                  value={form.afterAmount}
                  onChange={(e) => update("afterAmount", e.target.value)}
                  aria-invalid={!!errors.after || undefined}
                />
                <SelectRoot
                  value={form.afterUnit}
                  onValueChange={(v) => update("afterUnit", v as DurationUnit)}
                >
                  <SelectTrigger
                    aria-label="Duration unit"
                    className="w-[170px]"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(["h", "d", "m"] as DurationUnit[]).map((u) => (
                      <SelectItem key={u} value={u}>
                        {UNIT_LABEL[u]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
              </div>
              <p className={cn(T.hint, errors.after && "text-destructive")}>
                {errors.after ??
                  "Time since the object's creation timestamp before it expires."}
              </p>
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving || celBlocksSubmit}>
              {saving
                ? "Saving…"
                : mode === "create"
                  ? "Create rule"
                  : "Save changes"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
