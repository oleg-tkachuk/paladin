"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  PlayIcon,
  ShieldCheckIcon,
} from "@heroicons/react/24/outline";
import { ConnectError } from "@connectrpc/connect";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Skeleton } from "@/components/ui/Skeleton";
import { Badge } from "@/components/ui/badge";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import {
  bucketClient,
  objectKeyClient,
  policyClient,
  tenantClient,
} from "@/lib/connect/client";
import { useTenants } from "@/hooks/useTenants";
import { useBuckets } from "@/hooks/useBuckets";
import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useCedarValidation, hasCedarErrors } from "@/hooks/useCedarValidation";
import { CedarIndicator } from "@/components/ui/CedarIndicator";
import type { PolicyDiagnostic } from "@/gen/paladin/admin/v1/policy_service_pb";
import type { PolicyLayer } from "@/gen/paladin/admin/v1/policy_service_pb";
import { TestSuite } from "./_TestSuite";

// Curated Cedar policy starters surfaced via the "Load template"
// dropdown above the editor textarea. Pure UI constants — never round-
// tripped through the backend. Loading a template replaces the editor
// buffer (with a confirm dialog when there's unsaved content).
const POLICY_TEMPLATES: {
  id: string;
  label: string;
  description: string;
  cedar: string;
}[] = [
  {
    id: "read-only-auditor",
    label: "Read-only auditor",
    description:
      "Permits Get/List against any resource in the active tenant; forbids any write.",
    cedar: `permit (
  principal in Role::"tenant.auditor",
  action in [Action::"GetObject", Action::"ListObjects", Action::"GetTenant", Action::"ListBuckets"],
  resource
);
forbid (
  principal,
  action in [Action::"PutObject", Action::"DeleteObject", Action::"SetObjectTags", Action::"SetQuota"],
  resource
);`,
  },
  {
    id: "tenant-uploader",
    label: "Tenant uploader (bucket-scoped)",
    description:
      "Lets a service account PUT/Complete objects under one bucket only.",
    cedar: `permit (
  principal in Role::"tenant.uploader",
  action in [Action::"UploadObject", Action::"CompleteObject", Action::"PresignDownload"],
  resource in Bucket::"tenants/{tenant_id}/buckets/{bucket}"
);`,
  },
  {
    id: "agent-with-budget",
    label: "Agent with capability budget",
    description:
      "Restricts an agent principal to read+presign on one object_key, with capability caveats enforcing the budget separately.",
    cedar: `permit (
  principal in Role::"agent",
  action in [Action::"GetObject", Action::"PresignDownload", Action::"ListObjects"],
  resource in ObjectKey::"tenants/{tenant_id}/objectKeys/{object_key}"
);`,
  },
  {
    id: "deny-after-hours",
    label: "Deny destructive ops outside business hours",
    description:
      "Forbid blocks DeleteObject / DeleteBucket between 22:00–06:00 UTC. Pair with permits.",
    cedar: `forbid (
  principal,
  action in [Action::"DeleteObject", Action::"DeleteBucket", Action::"PurgeObject"],
  resource
)
when {
  context.now.getHours() < 6 || context.now.getHours() >= 22
};`,
  },
];

// Cedar policy editor: pick a scope (tenant / bucket / object_key), load
// its current cedar text + the merged effective stack, edit + validate
// + save. A separate Simulate panel calls PolicyService.SimulateAuthz to
// answer "is principal X allowed to do Y on Z?" without actually doing it.

type Scope = "tenant" | "bucket" | "objectKey";

const SCOPES: { value: Scope; label: string; help: string }[] = [
  {
    value: "tenant",
    label: "Tenant",
    help: "inherited_cedar_policy — applies to every bucket/object_key in the tenant",
  },
  {
    value: "bucket",
    label: "Bucket",
    help: "cedar_policy on a single bucket — layered on top of tenant policy",
  },
  {
    value: "objectKey",
    label: "Object Key",
    help: "cedar_policy at the object_key layer — most-specific resource policy",
  },
];

function bucketResourceName(backendId: string, bucketName: string) {
  return `storageBackends/${backendId}/buckets/${bucketName}`;
}

function tenantResourceName(tenantId: string) {
  return `tenants/${tenantId}`;
}

function objectKeyResourceName(tenantId: string, objectKey: string) {
  return `tenants/${tenantId}/objectKeys/${objectKey}`;
}

export default function PoliciesPage() {
  const { showNotification } = useNotification();

  // ─── scope + target selection ────────────────────────────────────────────
  const [scope, setScope] = useState<Scope>("tenant");
  const [target, setTarget] = useState<string>(""); // resource name

  const { tenants, fetchTenants, loading: tenantsLoading } = useTenants();
  const { buckets, fetchBuckets, loading: bucketsLoading } = useBuckets();
  const {
    objectKeys,
    fetchObjectKeys,
    loading: objectKeysLoading,
  } = useObjectKeys();

  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);
  useEffect(() => {
    if (scope === "bucket") void fetchBuckets();
    if (scope === "objectKey") void fetchObjectKeys();
  }, [scope, fetchBuckets, fetchObjectKeys]);

  // Reset target when scope changes.
  useEffect(() => {
    setTarget("");
  }, [scope]);

  const targetOptions = useMemo(() => {
    if (scope === "tenant") {
      return tenants.map((t) => ({
        value: tenantResourceName(t.tenantId),
        label: t.displayName ? `${t.displayName} (${t.tenantId})` : t.tenantId,
      }));
    }
    if (scope === "bucket") {
      return buckets.map((b) => ({
        value: bucketResourceName(b.backendId, b.bucketName),
        label: `${b.backendId}/${b.bucketName}`,
      }));
    }
    return objectKeys.map((k) => ({
      value: objectKeyResourceName(k.tenantId, k.objectKey),
      label: `${k.tenantId.slice(0, 8)}…/${k.objectKey}`,
    }));
  }, [scope, tenants, buckets, objectKeys]);

  // ─── policy state ────────────────────────────────────────────────────────
  const [policyText, setPolicyText] = useState("");
  const [resourceVersion, setResourceVersion] = useState("");
  const [loadingPolicy, setLoadingPolicy] = useState(false);
  const [saving, setSaving] = useState(false);

  const [diagnostics, setDiagnostics] = useState<PolicyDiagnostic[] | null>(
    null,
  );
  const [validating, setValidating] = useState(false);

  // Live Cedar validation runs continuously alongside the manual
  // Validate button. The button stays for the "force a re-check now"
  // affordance even when the live indicator already says valid; the
  // live indicator is what gates Save.
  const cedarState = useCedarValidation(policyText);
  const hasErrors = hasCedarErrors(cedarState);

  // Track the currently-loaded server snapshot so the "load template"
  // confirm dialog only fires when there's unsaved divergence. Reset
  // every time the target's policy is (re)loaded.
  const [serverSnapshot, setServerSnapshot] = useState("");
  const [templateId, setTemplateId] = useState<string>("");

  const [layers, setLayers] = useState<PolicyLayer[]>([]);
  const [merged, setMerged] = useState<string>("");
  const [loadingEffective, setLoadingEffective] = useState(false);

  const loadPolicyForTarget = useCallback(async () => {
    if (!target) {
      setPolicyText("");
      setServerSnapshot("");
      setResourceVersion("");
      setLayers([]);
      setMerged("");
      return;
    }
    setLoadingPolicy(true);
    try {
      let loaded = "";
      if (scope === "tenant") {
        const t = await tenantClient.getTenant({ name: target });
        loaded = t.inheritedCedarPolicy;
        setResourceVersion(t.resourceVersion);
      } else if (scope === "bucket") {
        const b = await bucketClient.getBucket({ name: target });
        loaded = b.cedarPolicy;
        setResourceVersion(b.resourceVersion);
      } else {
        const k = await objectKeyClient.getObjectKey({ name: target });
        loaded = k.cedarPolicy;
        setResourceVersion(k.resourceVersion);
      }
      setPolicyText(loaded);
      setServerSnapshot(loaded);
      setTemplateId("");
      setDiagnostics(null);
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Failed to load policy";
      showNotification({
        type: "error",
        title: "Load failed",
        message: msg,
      });
    } finally {
      setLoadingPolicy(false);
    }
  }, [scope, target, showNotification]);

  useEffect(() => {
    void loadPolicyForTarget();
  }, [loadPolicyForTarget]);

  const loadEffective = useCallback(async () => {
    if (!target) return;
    setLoadingEffective(true);
    try {
      const res = await policyClient.getEffectivePolicy({
        resourceName: target,
      });
      setMerged(res.mergedCedarPolicy);
      setLayers(res.layers);
    } catch (err) {
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to fetch effective policy";
      showNotification({
        type: "error",
        title: "Effective policy failed",
        message: msg,
      });
    } finally {
      setLoadingEffective(false);
    }
  }, [target, showNotification]);

  const handleValidate = useCallback(async () => {
    setValidating(true);
    try {
      const res = await policyClient.validate({ cedarPolicy: policyText });
      setDiagnostics(res.diagnostics);
      if (res.ok) {
        showNotification({
          type: "success",
          title: "Policy is valid",
          message: "No diagnostics returned by the Cedar parser.",
        });
      }
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Validate failed";
      showNotification({
        type: "error",
        title: "Validate failed",
        message: msg,
      });
    } finally {
      setValidating(false);
    }
  }, [policyText, showNotification]);

  // Pending template confirmation: when the editor buffer diverges
  // from the server snapshot, applying a template would clobber unsaved
  // work. Stash the candidate id and open <ConfirmModal> instead of
  // the native window.confirm() — same warning UX as the rest of the
  // app (Move to Trash / Restore / Revoke). Clean buffer applies
  // immediately with no dialog.
  const [pendingTemplateId, setPendingTemplateId] = useState<string | null>(
    null,
  );

  const applyTemplate = useCallback((id: string) => {
    const tpl = POLICY_TEMPLATES.find((t) => t.id === id);
    if (!tpl) return;
    setPolicyText(tpl.cedar);
    setTemplateId(tpl.id);
    setDiagnostics(null);
  }, []);

  const handleLoadTemplate = useCallback(
    (id: string) => {
      const tpl = POLICY_TEMPLATES.find((t) => t.id === id);
      if (!tpl) return;
      const dirty =
        policyText.trim().length > 0 && policyText !== serverSnapshot;
      if (dirty) {
        setPendingTemplateId(id);
        return;
      }
      applyTemplate(id);
    },
    [policyText, serverSnapshot, applyTemplate],
  );

  const pendingTemplate = useMemo(
    () =>
      pendingTemplateId
        ? POLICY_TEMPLATES.find((t) => t.id === pendingTemplateId)
        : null,
    [pendingTemplateId],
  );

  const handleSave = useCallback(async () => {
    if (!target) return;
    setSaving(true);
    try {
      if (scope === "tenant") {
        const updated = await tenantClient.setInheritedPolicy({
          name: target,
          resourceVersion,
          cedarPolicy: policyText,
        });
        setResourceVersion(updated.resourceVersion);
      } else if (scope === "bucket") {
        const updated = await bucketClient.setBucketPolicy({
          name: target,
          resourceVersion,
          cedarPolicy: policyText,
        });
        setResourceVersion(updated.resourceVersion);
      } else {
        const updated = await objectKeyClient.setObjectKeyPolicy({
          name: target,
          resourceVersion,
          cedarPolicy: policyText,
        });
        setResourceVersion(updated.resourceVersion);
      }
      setServerSnapshot(policyText);
      showNotification({
        type: "success",
        title: "Policy saved",
        message: target,
      });
    } catch (err) {
      const msg = err instanceof ConnectError ? err.rawMessage : "Save failed";
      showNotification({
        type: "error",
        title: "Save failed",
        message: msg,
      });
    } finally {
      setSaving(false);
    }
  }, [scope, target, resourceVersion, policyText, showNotification]);

  const targetLoading =
    (scope === "tenant" && tenantsLoading) ||
    (scope === "bucket" && bucketsLoading) ||
    (scope === "objectKey" && objectKeysLoading);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Policies"
        description="Cedar policies attached to tenants, buckets, and object keys."
      />

      <Card className="space-y-4 p-4">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-[260px_1fr_auto]">
          <div className="space-y-1.5">
            <Label>Scope</Label>
            <SelectRoot
              value={scope}
              onValueChange={(v) => setScope(v as Scope)}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SCOPES.map((s) => (
                  <SelectItem key={s.value} value={s.value}>
                    {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
            <p className="text-xs text-muted-foreground">
              {SCOPES.find((s) => s.value === scope)?.help}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label>Target</Label>
            <Select
              options={targetOptions}
              value={target}
              onChange={setTarget}
              placeholder={
                targetLoading
                  ? "Loading…"
                  : targetOptions.length === 0
                    ? `No ${scope}s available`
                    : "Pick a target…"
              }
              className="w-full"
              disabled={targetLoading || targetOptions.length === 0}
            />
            {target && (
              <p className="font-mono text-xs text-muted-foreground">
                {target}
              </p>
            )}
          </div>

          <div className="flex items-end">
            <Button
              variant="outline"
              size="icon"
              onClick={() => void loadPolicyForTarget()}
              disabled={!target || loadingPolicy}
              aria-label="Reload policy"
            >
              <ArrowPathIcon
                className={cn("size-4", loadingPolicy && "animate-spin")}
              />
            </Button>
          </div>
        </div>
      </Card>

      <Tabs defaultValue="editor">
        <TabsList>
          <TabsTrigger value="editor">
            <ShieldCheckIcon className="size-4" />
            Editor
          </TabsTrigger>
          <TabsTrigger value="effective">Effective</TabsTrigger>
          <TabsTrigger value="simulate">
            <PlayIcon className="size-4" />
            Simulate
          </TabsTrigger>
        </TabsList>

        <TabsContent value="editor" className="space-y-4">
          <Card className="space-y-3 p-4">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-[1fr_auto] md:items-end">
              <div className="space-y-1.5">
                <Label htmlFor="policy-template">Load template</Label>
                <SelectRoot
                  value={templateId}
                  onValueChange={handleLoadTemplate}
                >
                  <SelectTrigger id="policy-template" className="w-full">
                    <SelectValue placeholder="Pick a starter…" />
                  </SelectTrigger>
                  <SelectContent>
                    {POLICY_TEMPLATES.map((t) => (
                      <SelectItem key={t.id} value={t.id}>
                        {t.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
                {templateId && (
                  <p className="text-xs text-muted-foreground">
                    {
                      POLICY_TEMPLATES.find((t) => t.id === templateId)
                        ?.description
                    }
                  </p>
                )}
              </div>
              <div className="flex items-end gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleValidate}
                  disabled={validating || !policyText}
                >
                  {validating ? "Validating…" : "Validate"}
                </Button>
                <Button
                  size="sm"
                  onClick={handleSave}
                  disabled={saving || !target || hasErrors}
                >
                  {saving ? "Saving…" : "Save"}
                </Button>
              </div>
            </div>
            <Label htmlFor="policy-textarea">Cedar policy</Label>
            {loadingPolicy ? (
              <Skeleton className="h-64 w-full" />
            ) : (
              <Textarea
                id="policy-textarea"
                value={policyText}
                onChange={(e) => setPolicyText(e.target.value)}
                placeholder={
                  target
                    ? "// permit ( principal, action, resource );"
                    : "Pick a target above to load its policy."
                }
                disabled={!target}
                className="min-h-[280px] font-mono text-xs leading-relaxed"
                spellCheck={false}
              />
            )}
            <CedarIndicator state={cedarState} />
            {diagnostics !== null && diagnostics.length === 0 && (
              <div className="flex items-center gap-2 rounded-md border border-emerald-500/40 bg-emerald-500/10 p-2 text-sm text-emerald-700 dark:text-emerald-400">
                <CheckCircleIcon className="size-4" />
                Policy parses cleanly.
              </div>
            )}
            {diagnostics !== null && diagnostics.length > 0 && (
              <div className="space-y-1">
                {diagnostics.map((d, i) => (
                  <DiagnosticRow key={i} diag={d} />
                ))}
              </div>
            )}
            {target && (
              <p className="text-xs text-muted-foreground">
                resourceVersion:{" "}
                <span className="font-mono">{resourceVersion || "—"}</span>
              </p>
            )}
          </Card>
        </TabsContent>

        <TabsContent value="effective" className="space-y-4">
          <Card className="space-y-3 p-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-medium">Merged policy stack</p>
                <p className="text-xs text-muted-foreground">
                  Tenant → bucket → object_key, evaluated in that order. Use
                  this view to debug why a request was allowed or denied.
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => void loadEffective()}
                disabled={!target || loadingEffective}
              >
                {loadingEffective ? "Loading…" : "Compute"}
              </Button>
            </div>
            {layers.length > 0 && (
              <div className="space-y-2">
                {layers.map((layer, i) => (
                  <div
                    key={`${layer.source}-${i}`}
                    className="rounded-md border bg-muted/30 p-2"
                  >
                    <div className="mb-1 flex items-center gap-2 text-xs">
                      <Badge variant="outline" className="font-mono">
                        {layer.source || "(unknown)"}
                      </Badge>
                    </div>
                    <pre
                      className={cn(
                        T.codeSmall,
                        "overflow-x-auto whitespace-pre-wrap leading-relaxed text-muted-foreground",
                      )}
                    >
                      {layer.cedarPolicy || "(empty)"}
                    </pre>
                  </div>
                ))}
              </div>
            )}
            {merged && (
              <div>
                <Label className="text-xs">Merged</Label>
                <pre
                  className={cn(
                    T.codeSmall,
                    "mt-1 max-h-[320px] overflow-auto rounded-md border bg-muted/30 p-3 leading-relaxed",
                  )}
                >
                  {merged}
                </pre>
              </div>
            )}
            {!loadingEffective && layers.length === 0 && !merged && (
              <p className="text-sm text-muted-foreground">
                Pick a target and click <em>Compute</em> to see the merged
                stack.
              </p>
            )}
          </Card>
        </TabsContent>

        <TabsContent value="simulate" className="space-y-4">
          <TestSuite defaultResource={target} />
        </TabsContent>
      </Tabs>

      {/* ─── Load-template confirmation ───────────────────────────────
          Mirrors the warn-before-clobber pattern used by the object
          inspector / capability revoke flows. Type=warning because
          dropping unsaved edits is recoverable from git / muscle
          memory but not from the editor itself. */}
      <ConfirmModal
        isOpen={pendingTemplate !== null}
        onClose={() => setPendingTemplateId(null)}
        onConfirm={() => {
          if (pendingTemplateId) applyTemplate(pendingTemplateId);
          setPendingTemplateId(null);
        }}
        title="Replace editor contents?"
        message={
          pendingTemplate
            ? `Loading the "${pendingTemplate.label}" template will replace your unsaved Cedar edits. This cannot be undone from the UI.`
            : ""
        }
        type="warning"
        confirmText="Load template"
        cancelText="Keep editing"
      />
    </div>
  );
}

// ─── Diagnostic row ────────────────────────────────────────────────────────

function DiagnosticRow({ diag }: { diag: PolicyDiagnostic }) {
  const isError = diag.severity.toLowerCase() === "error";
  const Icon = isError ? ExclamationTriangleIcon : CheckCircleIcon;
  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-md border p-2 text-sm",
        isError
          ? "border-destructive/40 bg-destructive/10 text-destructive"
          : "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400",
      )}
    >
      <Icon className="mt-0.5 size-4 shrink-0" />
      <div className="space-y-0.5">
        <p>{diag.message}</p>
        {(diag.line > 0 || diag.column > 0) && (
          <p className="font-mono text-xs opacity-70">
            line {diag.line}, col {diag.column}
          </p>
        )}
      </div>
    </div>
  );
}
