"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import {
  ArrowLeftIcon,
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  KeyIcon,
  LinkIcon,
} from "@heroicons/react/24/outline";
import { ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";

import { objectKeyClient, policyClient } from "@/lib/connect/client";
import { useBuckets } from "@/hooks/useBuckets";
import { useAuth } from "@/context/AuthContext";
import { ObjectKeySchema, type ObjectKey } from "@/gen/paladin/admin/v1/types_pb";
import { CompletionMode } from "@/gen/paladin/common/v1/resource_pb";
import type { PolicyDiagnostic } from "@/gen/paladin/admin/v1/policy_service_pb";
import { cn } from "@/lib/utils";

// Detail editor for a single ObjectKey: identity, bucket binding, and the
// per-resource Cedar policy. Tenant scope comes from the signed-in user;
// platform admins acting cross-tenant should jump in via /policies and
// pick the ObjectKey scope explicitly.

function objectKeyResourceName(tenantId: string, objectKey: string) {
  return `tenants/${tenantId}/objectKeys/${objectKey}`;
}

function completionModeLabel(m: CompletionMode): string {
  switch (m) {
    case CompletionMode.IMPLICIT:
      return "implicit (S3 events)";
    case CompletionMode.EXPLICIT:
      return "explicit (CompleteObject)";
    default:
      return "unspecified";
  }
}

function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  if (!ts) return "—";
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return "—";
  try {
    return new Date(ms).toISOString().replace("T", " ").replace(".000Z", "Z");
  } catch {
    return "—";
  }
}

export default function ObjectKeyDetailPage() {
  const params = useParams();
  const router = useRouter();
  const objectKeyName = (params.name as string) ?? "";

  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const resourceName = tenantId
    ? objectKeyResourceName(tenantId, objectKeyName)
    : "";

  const { showNotification } = useNotification();
  const { buckets, fetchBuckets } = useBuckets();

  const [ok, setOk] = useState<ObjectKey | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Display name editor
  const [displayName, setDisplayName] = useState("");
  const [savingMeta, setSavingMeta] = useState(false);

  // Bucket binding editor
  const [bucketSelection, setBucketSelection] = useState("");
  const [binding, setBinding] = useState(false);

  // Cedar policy editor
  const [policyText, setPolicyText] = useState("");
  const [validating, setValidating] = useState(false);
  const [savingPolicy, setSavingPolicy] = useState(false);
  const [diagnostics, setDiagnostics] = useState<PolicyDiagnostic[] | null>(
    null,
  );

  const load = useCallback(async () => {
    if (!resourceName) return;
    setLoading(true);
    setError(null);
    try {
      const fresh = await objectKeyClient.getObjectKey({ name: resourceName });
      setOk(fresh);
      setDisplayName(fresh.displayName);
      setBucketSelection(fresh.bucket);
      setPolicyText(fresh.cedarPolicy);
      setDiagnostics(null);
    } catch (err) {
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to load object key";
      setError(msg);
    } finally {
      setLoading(false);
    }
  }, [resourceName]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void fetchBuckets();
  }, [fetchBuckets]);

  // ─── handlers ───────────────────────────────────────────────────────────

  const handleSaveDisplayName = useCallback(async () => {
    if (!ok) return;
    setSavingMeta(true);
    try {
      const objectKeyResource = create(ObjectKeySchema, {
        name: ok.name,
        tenantId: ok.tenantId,
        objectKey: ok.objectKey,
        displayName,
        bucket: ok.bucket,
        cedarPolicy: ok.cedarPolicy,
        resourceVersion: ok.resourceVersion,
      });
      const updated = await objectKeyClient.updateObjectKey({
        name: ok.name,
        resourceVersion: ok.resourceVersion,
        updateMask: create(FieldMaskSchema, { paths: ["display_name"] }),
        objectKeyResource,
      });
      setOk(updated);
      showNotification({
        type: "success",
        title: "Display name updated",
        message: updated.displayName || updated.objectKey,
      });
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Update failed";
      showNotification({
        type: "error",
        title: "Update failed",
        message: msg,
      });
    } finally {
      setSavingMeta(false);
    }
  }, [ok, displayName, showNotification]);

  const handleBind = useCallback(async () => {
    if (!ok || !bucketSelection || bucketSelection === ok.bucket) return;
    setBinding(true);
    try {
      const updated = await objectKeyClient.bindObjectKeyToBucket({
        name: ok.name,
        resourceVersion: ok.resourceVersion,
        bucket: bucketSelection,
      });
      setOk(updated);
      showNotification({
        type: "success",
        title: "Bucket re-bound",
        message: bucketSelection,
      });
    } catch (err) {
      const msg = err instanceof ConnectError ? err.rawMessage : "Bind failed";
      showNotification({
        type: "error",
        title: "Bind failed",
        message: msg,
      });
    } finally {
      setBinding(false);
    }
  }, [ok, bucketSelection, showNotification]);

  const handleValidate = useCallback(async () => {
    setValidating(true);
    try {
      const res = await policyClient.validate({ cedarPolicy: policyText });
      setDiagnostics(res.diagnostics);
      if (res.ok) {
        showNotification({
          type: "success",
          title: "Policy is valid",
          message: "No diagnostics returned.",
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

  const handleSavePolicy = useCallback(async () => {
    if (!ok) return;
    setSavingPolicy(true);
    try {
      const updated = await objectKeyClient.setObjectKeyPolicy({
        name: ok.name,
        resourceVersion: ok.resourceVersion,
        cedarPolicy: policyText,
      });
      setOk(updated);
      showNotification({
        type: "success",
        title: "Policy saved",
        message: ok.objectKey,
      });
    } catch (err) {
      const msg = err instanceof ConnectError ? err.rawMessage : "Save failed";
      showNotification({
        type: "error",
        title: "Save failed",
        message: msg,
      });
    } finally {
      setSavingPolicy(false);
    }
  }, [ok, policyText, showNotification]);

  // ─── render ─────────────────────────────────────────────────────────────

  if (!loading && error) {
    return (
      <div className="space-y-6">
        <PageHeader
          title={
            <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
              <Button
                variant="ghost"
                size="icon"
                onClick={() => router.push("/object-keys")}
                aria-label="Back"
              >
                <ArrowLeftIcon className="size-5" />
              </Button>
              <span className="truncate font-mono text-xl">
                {objectKeyName}
              </span>
            </h1>
          }
          description="Object Key detail"
          showDefaultActions={false}
        />
        <Card className="flex flex-col items-center gap-3 p-12 text-center">
          <ExclamationTriangleIcon className="h-10 w-10 text-destructive" />
          <div className="text-sm font-medium">Unable to load</div>
          <div className="max-w-md text-sm text-muted-foreground">{error}</div>
          <Button variant="outline" size="sm" onClick={() => void load()}>
            <ArrowPathIcon className="size-4" />
            Retry
          </Button>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={
          <h1 className="flex items-center gap-3 truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => router.push("/object-keys")}
              aria-label="Back to Object Keys"
            >
              <ArrowLeftIcon className="size-5" />
            </Button>
            <KeyIcon className="size-6 text-primary" />
            <span className="truncate font-mono text-xl">{objectKeyName}</span>
          </h1>
        }
        description={ok?.displayName || "Tenant-scoped namespace"}
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void load()}
            disabled={loading}
          >
            <ArrowPathIcon
              className={cn("size-4", loading && "animate-spin")}
            />
            Refresh
          </Button>
        }
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* ─── Identity ────────────────────────────────────────────────── */}
        <Card className="space-y-3 p-4">
          <h2 className="text-sm font-semibold">Identity</h2>
          {loading || !ok ? (
            <Skeleton className="h-32 w-full" />
          ) : (
            <dl className="grid grid-cols-[140px_1fr] gap-x-3 gap-y-2 text-sm">
              <dt className="text-muted-foreground">Resource name</dt>
              <dd className="break-all font-mono text-xs">{ok.name}</dd>

              <dt className="text-muted-foreground">Tenant ID</dt>
              <dd className="break-all font-mono text-xs">{ok.tenantId}</dd>

              <dt className="text-muted-foreground">Object key</dt>
              <dd className="font-mono text-xs">{ok.objectKey}</dd>

              <dt className="text-muted-foreground">Completion mode</dt>
              <dd>
                <Badge variant="outline" className="font-mono text-[11px]">
                  {completionModeLabel(ok.completionMode)}
                </Badge>
              </dd>

              <dt className="text-muted-foreground">Resource version</dt>
              <dd className="font-mono text-xs">{ok.resourceVersion || "—"}</dd>

              <dt className="text-muted-foreground">Created</dt>
              <dd className="font-mono text-xs">
                {formatTimestamp(ok.createdAt)}
              </dd>

              <dt className="text-muted-foreground">Updated</dt>
              <dd className="font-mono text-xs">
                {formatTimestamp(ok.updatedAt)}
              </dd>
            </dl>
          )}

          <div className="space-y-1.5 pt-2">
            <Label htmlFor="ok-display-name">Display name</Label>
            <div className="flex gap-2">
              <Input
                id="ok-display-name"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder="Production assets"
                disabled={!ok || loading}
              />
              <Button
                size="sm"
                onClick={handleSaveDisplayName}
                disabled={
                  !ok || savingMeta || displayName === (ok?.displayName ?? "")
                }
              >
                {savingMeta ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        </Card>

        {/* ─── Bucket binding ──────────────────────────────────────────── */}
        <Card className="space-y-3 p-4">
          <div className="flex items-center gap-2">
            <LinkIcon className="size-4 text-muted-foreground" />
            <h2 className="text-sm font-semibold">Bucket binding</h2>
          </div>
          <p className="text-xs text-muted-foreground">
            Re-binding triggers an audit entry — pick a destination bucket and
            confirm. Existing objects keep their physical location until
            migrated.
          </p>
          {loading || !ok ? (
            <Skeleton className="h-20 w-full" />
          ) : (
            <>
              <div className="space-y-1">
                <Label className="text-xs">Current</Label>
                <p className="break-all font-mono text-xs">
                  {ok.bucket || (
                    <span className="italic text-muted-foreground">
                      (unbound)
                    </span>
                  )}
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="ok-bucket-binding">Bind to bucket</Label>
                <SelectRoot
                  value={bucketSelection}
                  onValueChange={setBucketSelection}
                >
                  <SelectTrigger id="ok-bucket-binding" className="w-full">
                    <SelectValue placeholder="Pick a bucket…" />
                  </SelectTrigger>
                  <SelectContent>
                    {buckets.map((b) => {
                      const value = `storageBackends/${b.backendId}/buckets/${b.bucketName}`;
                      return (
                        <SelectItem key={value} value={value}>
                          <span className="font-mono text-xs">
                            {b.backendId}/{b.bucketName}
                          </span>
                          {b.displayName ? (
                            <span className="text-muted-foreground">
                              {" "}
                              — {b.displayName}
                            </span>
                          ) : null}
                        </SelectItem>
                      );
                    })}
                  </SelectContent>
                </SelectRoot>
              </div>
              <div className="flex justify-end">
                <Button
                  size="sm"
                  onClick={handleBind}
                  disabled={
                    binding || !bucketSelection || bucketSelection === ok.bucket
                  }
                >
                  {binding ? "Binding…" : "Re-bind"}
                </Button>
              </div>
            </>
          )}
        </Card>
      </div>

      {/* ─── Cedar policy ────────────────────────────────────────────── */}
      <Card className="space-y-3 p-4">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold">
            Cedar policy{" "}
            <Link
              href="/policies"
              className="ml-2 text-xs font-normal text-muted-foreground hover:text-primary hover:underline"
            >
              open in editor →
            </Link>
          </h2>
          <div className="flex gap-2">
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
              onClick={handleSavePolicy}
              disabled={savingPolicy || !ok}
            >
              {savingPolicy ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
        {loading ? (
          <Skeleton className="h-48 w-full" />
        ) : (
          <Textarea
            value={policyText}
            onChange={(e) => setPolicyText(e.target.value)}
            placeholder="// permit ( principal, action, resource );"
            className="min-h-[220px] font-mono text-xs leading-relaxed"
            spellCheck={false}
          />
        )}
        {diagnostics !== null && diagnostics.length === 0 && (
          <div className="flex items-center gap-2 rounded-md border border-emerald-500/40 bg-emerald-500/10 p-2 text-sm text-emerald-700 dark:text-emerald-400">
            <CheckCircleIcon className="size-4" />
            Policy parses cleanly.
          </div>
        )}
        {diagnostics !== null && diagnostics.length > 0 && (
          <div className="space-y-1">
            {diagnostics.map((d, i) => {
              const isError = d.severity.toLowerCase() === "error";
              return (
                <div
                  key={i}
                  className={cn(
                    "flex items-start gap-2 rounded-md border p-2 text-sm",
                    isError
                      ? "border-destructive/40 bg-destructive/10 text-destructive"
                      : "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400",
                  )}
                >
                  <ExclamationTriangleIcon className="mt-0.5 size-4 shrink-0" />
                  <div>
                    <p>{d.message}</p>
                    {(d.line > 0 || d.column > 0) && (
                      <p className="font-mono text-xs opacity-70">
                        line {d.line}, col {d.column}
                      </p>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>
    </div>
  );
}
