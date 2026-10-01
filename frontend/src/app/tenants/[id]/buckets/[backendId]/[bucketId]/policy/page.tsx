"use client";

// Bucket-level Cedar policy editor. The engine compiles this layer, after the
// tenant's and before the collection's, into every request on a collection
// bound to this bucket. Mirrors the Collection Policy tab almost
// verbatim; the only differences are which client + RPC and which
// resource the editor maps to.

import { useCallback, useState } from "react";
import Link from "next/link";
import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";

import { bucketClient, policyClient } from "@/lib/connect/client";
import type { PolicyDiagnostic } from "@/gen/paladin/admin/v1/policy_service_pb";
import { cn } from "@/lib/utils";

import { useBucket } from "../bucket-context";
import { errorMessage } from "@/hooks/errorContract";

export default function BucketPolicyPage() {
  const { bucket, setBucket } = useBucket();
  const { showNotification } = useNotification();

  const [policyText, setPolicyText] = useState(bucket.cedarPolicy);
  const [validating, setValidating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [diagnostics, setDiagnostics] = useState<PolicyDiagnostic[] | null>(
    null,
  );

  // Reseed editor state when the context's bucket changes (another tab wrote
  // and refreshed the cache, or refetch fired). Render-phase adjust-on-change
  // (not set-state-in-effect) — `bucket` identity is stable between renders
  // via TanStack structural sharing, so this fires only on a real change.
  // Diagnostics clear on re-seed so a stale "valid" pill doesn't linger.
  const [seededBucket, setSeededBucket] = useState(bucket);
  if (bucket !== seededBucket) {
    setSeededBucket(bucket);
    setPolicyText(bucket.cedarPolicy);
    setDiagnostics(null);
  }

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
      const msg = errorMessage(err, "Validate failed");
      showNotification({
        type: "error",
        title: "Validate failed",
        message: msg,
      });
    } finally {
      setValidating(false);
    }
  }, [policyText, showNotification]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      const updated = await bucketClient.setBucketPolicy({
        name: bucket.name,
        resourceVersion: bucket.resourceVersion,
        cedarPolicy: policyText,
      });
      setBucket(updated);
      showNotification({
        type: "success",
        title: "Policy saved",
        message: bucket.bucketId,
      });
    } catch (err) {
      const msg = errorMessage(err, "Save failed");
      showNotification({ type: "error", title: "Save failed", message: msg });
    } finally {
      setSaving(false);
    }
  }, [bucket, policyText, setBucket, showNotification]);

  return (
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
          <Button size="sm" onClick={handleSave} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>
      {bucket.resourceVersion === "" ? (
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
  );
}
