"use client";

// Lifecycle rules CRUD for a single bucket. Dedicated route rather than
// a tab inside a bucket detail page because the detail-page surface
// doesn't exist yet (buckets is currently a flat table). Bucket
// identity needs two segments (backendId + bucketName), so a nested
// route is the natural shape.
//
// Backend semantics:
//   - SetLifecycleRules is a destructive replace. Edit/Toggle/Delete
//     all build the full desired list locally and ship it.
//   - Optimistic concurrency via resourceVersion — on Aborted we
//     refetch and ask the operator to retry.

import React, { useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";
import type { Duration } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  ChevronLeftIcon,
  ClockIcon,
  EllipsisHorizontalIcon,
  ExclamationTriangleIcon,
  PencilIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/Skeleton";
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
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";

import { bucketClient } from "@/lib/connect/client";
import {
  type Bucket,
  type LifecycleRule,
  LifecycleRuleSchema,
  LifecycleTransitionSchema,
  LifecycleExpirationSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

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

/**
 * Pick the largest unit that divides cleanly. e.g. 86400 → "1d", 3600
 * → "1h", 2_592_000 → "1m". Fallback to days with a fractional cap.
 */
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
  // odd value — round down to days. Rare; only occurs if a CLI/API
  // operator wrote a non-canonical Duration.
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

type ActionKind = "transition" | "expiration";

interface RuleFormState {
  id: string;
  enabled: boolean;
  match: string;
  action: ActionKind;
  afterAmount: string;
  afterUnit: DurationUnit;
  storageClass: string;
}

const DEFAULT_FORM: RuleFormState = {
  id: "",
  enabled: true,
  match: "",
  action: "expiration",
  afterAmount: "30",
  afterUnit: "d",
  storageClass: "",
};

const RULE_ID_REGEX = /^[a-z][a-z0-9-]{0,62}$/;

interface FormErrors {
  id?: string;
  match?: string;
  after?: string;
  storageClass?: string;
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
  if (form.action === "transition" && !form.storageClass.trim()) {
    errors.storageClass = "Storage class is required for transitions.";
  }
  return errors;
}

function ruleToForm(rule: LifecycleRule): RuleFormState {
  if (rule.action.case === "transition") {
    const t = rule.action.value;
    const { amount, unit } = decomposeDuration(t.after);
    return {
      id: rule.id,
      enabled: rule.enabled,
      match: rule.match,
      action: "transition",
      afterAmount: String(amount),
      afterUnit: unit,
      storageClass: t.storageClass,
    };
  }
  if (rule.action.case === "expiration") {
    const e = rule.action.value;
    const { amount, unit } = decomposeDuration(e.after);
    return {
      id: rule.id,
      enabled: rule.enabled,
      match: rule.match,
      action: "expiration",
      afterAmount: String(amount),
      afterUnit: unit,
      storageClass: "",
    };
  }
  return { ...DEFAULT_FORM, id: rule.id, enabled: rule.enabled };
}

function formToRule(form: RuleFormState): LifecycleRule {
  const after = buildDuration(Number(form.afterAmount), form.afterUnit);
  const action: LifecycleRule["action"] =
    form.action === "transition"
      ? {
          case: "transition",
          value: create(LifecycleTransitionSchema, {
            after,
            storageClass: form.storageClass.trim(),
          }),
        }
      : {
          case: "expiration",
          value: create(LifecycleExpirationSchema, { after }),
        };
  return create(LifecycleRuleSchema, {
    id: form.id.trim(),
    enabled: form.enabled,
    match: form.match.trim(),
    action,
  });
}

// ─── page ────────────────────────────────────────────────────────────────────

export default function BucketLifecyclePage() {
  const params = useParams<{ backendId: string; bucketName: string }>();
  const backendId = decodeURIComponent(params?.backendId ?? "");
  const bucketName = decodeURIComponent(params?.bucketName ?? "");
  const resourceName = `storageBackends/${backendId}/buckets/${bucketName}`;

  const { showNotification } = useNotification();

  const [bucket, setBucket] = useState<Bucket | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Editor dialog state.
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorMode, setEditorMode] = useState<"create" | "edit">("create");
  const [form, setForm] = useState<RuleFormState>(DEFAULT_FORM);
  const [errors, setErrors] = useState<FormErrors>({});

  // Delete confirmation state.
  const [deleteTarget, setDeleteTarget] = useState<LifecycleRule | null>(null);

  const fetchBucket = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const fresh = await bucketClient.getBucket({ name: resourceName });
      setBucket(fresh);
      return fresh;
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Failed to load bucket.";
      setLoadError(msg);
      throw err;
    } finally {
      setLoading(false);
    }
  }, [resourceName]);

  useEffect(() => {
    fetchBucket().catch(() => {
      /* error already surfaced via loadError */
    });
  }, [fetchBucket]);

  const rules = bucket?.lifecycleRules ?? [];

  // Persist `nextRules` via SetLifecycleRules. On Aborted (stale
  // resourceVersion) we refetch and prompt the operator to retry —
  // safer than blind retries that could clobber a concurrent edit.
  const persistRules = useCallback(
    async (
      nextRules: LifecycleRule[],
      successMessage: string,
    ): Promise<boolean> => {
      if (!bucket) return false;
      setSaving(true);
      try {
        const updated = await bucketClient.setLifecycleRules({
          name: resourceName,
          resourceVersion: bucket.resourceVersion,
          rules: nextRules,
        });
        setBucket(updated);
        showNotification({ type: "success", title: successMessage });
        return true;
      } catch (err) {
        if (err instanceof ConnectError && err.code === Code.Aborted) {
          await fetchBucket().catch(() => {});
          showNotification({
            type: "error",
            title: "Bucket changed elsewhere",
            message: "Reloaded the latest version — please retry your change.",
          });
          return false;
        }
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to save lifecycle rules.";
        showNotification({ type: "error", title: "Save failed", message: msg });
        return false;
      } finally {
        setSaving(false);
      }
    },
    [bucket, fetchBucket, resourceName, showNotification],
  );

  // ─── handlers ──────────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditorMode("create");
    setForm(DEFAULT_FORM);
    setErrors({});
    setEditorOpen(true);
  };

  const openEdit = (rule: LifecycleRule) => {
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
    // Optimistic update — flip the row, persist, revert on failure.
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

  // ─── render ────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-6">
      <PageHeader
        title={
          <div className="space-y-1">
            <Link
              href="/buckets"
              className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
            >
              <ChevronLeftIcon className="size-4" />
              All buckets
            </Link>
            <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
              Lifecycle rules
            </h1>
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="info" className={T.code}>
                {backendId}
              </Badge>
              <span className={T.codeSmall}>{bucketName}</span>
            </div>
          </div>
        }
        description="Automate transitions and expirations of objects based on age and a CEL filter."
        showDefaultActions={false}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="icon"
              onClick={() => fetchBucket().catch(() => {})}
              aria-label="Refresh"
              disabled={loading}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
            </Button>
            <Button size="sm" onClick={openCreate} disabled={!bucket}>
              <PlusIcon className="size-4" />
              Add rule
            </Button>
          </div>
        }
      />

      {loadError && !bucket ? (
        <Card className="flex flex-col items-center gap-3 p-10 text-center">
          <ExclamationTriangleIcon className="size-10 text-destructive" />
          <p className={T.body}>{loadError}</p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => fetchBucket().catch(() => {})}
          >
            Retry
          </Button>
        </Card>
      ) : loading && !bucket ? (
        <div className="space-y-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-20 w-full" />
          ))}
        </div>
      ) : rules.length === 0 ? (
        <Card className="flex flex-col items-center gap-3 p-10 text-center">
          <ClockIcon className="size-10 text-muted-foreground" />
          <h2 className={T.cardTitleProse}>No lifecycle rules</h2>
          <p className={cn(T.helper, "max-w-md")}>
            Lifecycle rules automate transitions and expirations of objects
            based on age and a CEL filter. Background workers evaluate rules on
            the configured cadence.
          </p>
          <Button size="sm" onClick={openCreate}>
            <PlusIcon className="size-4" />
            Add rule
          </Button>
        </Card>
      ) : (
        <div className="space-y-3">
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

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this rule?</AlertDialogTitle>
            <AlertDialogDescription>
              Removing rule{" "}
              <span className="font-mono text-foreground">
                {deleteTarget?.id}
              </span>
              . Objects already affected by previous evaluations are not
              reverted; only future evaluations are skipped.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete rule
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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
    return `transition to ${rule.action.value.storageClass || "?"} after ${formatDuration(rule.action.value.after)}`;
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
  // Card is fully clickable (opens editor). The kebab is in a stop-prop
  // wrapper so its dropdown items don't bubble up as a card click.
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
            <Badge
              variant={rule.enabled ? "success" : "outline"}
              className={T.labelTight}
            >
              {rule.enabled ? "enabled" : "disabled"}
            </Badge>
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
              <DropdownMenuItem onSelect={onEdit} disabled={busy}>
                <PencilIcon className="size-4" />
                Edit
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={onToggle} disabled={busy}>
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

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <DialogContent className="sm:max-w-[560px]">
        <form onSubmit={onSubmit}>
          <DialogHeader>
            <DialogTitle>
              {mode === "create" ? "New lifecycle rule" : "Edit lifecycle rule"}
            </DialogTitle>
            <DialogDescription>
              {mode === "create"
                ? "Filter objects with a CEL expression and choose either a storage-class transition or an expiration."
                : "Tune the match expression or action. The rule ID cannot be changed."}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {/* ID + enabled */}
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

            {/* CEL match */}
            <div className="space-y-1.5">
              <Label htmlFor="rule-match">Match (CEL)</Label>
              <Textarea
                id="rule-match"
                className="font-mono text-xs min-h-[80px]"
                placeholder="object.size_bytes > 1_000_000"
                value={form.match}
                onChange={(e) => update("match", e.target.value)}
                aria-invalid={!!errors.match || undefined}
              />
              <p className={cn(T.hint, errors.match && "text-destructive")}>
                {errors.match ??
                  "Evaluated against object fields. See the CEL reference for the available identifiers."}
              </p>
            </div>

            {/* Action picker */}
            <div className="space-y-1.5">
              <Label>Action</Label>
              <div className="grid grid-cols-2 gap-2">
                <ActionToggle
                  active={form.action === "expiration"}
                  onClick={() => update("action", "expiration")}
                  title="Expiration"
                  description="Delete the object."
                />
                <ActionToggle
                  active={form.action === "transition"}
                  onClick={() => update("action", "transition")}
                  title="Transition"
                  description="Move to a different storage class."
                />
              </div>
            </div>

            {/* After (duration) */}
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
                  <SelectTrigger className="w-[170px]">
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
                  "Time since the object's creation timestamp before the action fires."}
              </p>
            </div>

            {form.action === "transition" && (
              <div className="space-y-1.5">
                <Label htmlFor="rule-storage-class">Storage class</Label>
                <Input
                  id="rule-storage-class"
                  className="font-mono text-xs"
                  placeholder="GLACIER"
                  value={form.storageClass}
                  onChange={(e) => update("storageClass", e.target.value)}
                  aria-invalid={!!errors.storageClass || undefined}
                />
                <p
                  className={cn(
                    T.hint,
                    errors.storageClass && "text-destructive",
                  )}
                >
                  {errors.storageClass ??
                    "Backend-defined identifier. Free-text — backends validate at apply time."}
                </p>
              </div>
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
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

// Inline radio-style toggle — reuses the existing button + ring pattern
// rather than introducing a new RadioGroup primitive.
function ActionToggle({
  active,
  onClick,
  title,
  description,
}: {
  active: boolean;
  onClick: () => void;
  title: string;
  description: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "rounded-md border p-3 text-left transition-colors",
        active
          ? "border-primary bg-primary/5 ring-1 ring-primary"
          : "border-border hover:border-foreground/30",
      )}
    >
      <div className="text-sm font-medium text-foreground">{title}</div>
      <div className={T.hint}>{description}</div>
    </button>
  );
}
