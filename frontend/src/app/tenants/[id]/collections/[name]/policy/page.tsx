"use client";

// Collection-level Cedar policy editor. The cross-tenant /policies
// page is still the broader editor (lets you switch principal /
// resource scope freely); this surface is the focused one for an
// operator already inside the Collection context.

import { Confirmation } from "@/components/ui/Confirmation";
import { useConfirmation } from "@/hooks/useConfirmation";
import { useTenantChangesBlocked } from "../../../tenant-context";
import { useCallback, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  ShieldCheckIcon,
} from "@heroicons/react/24/outline";

import { Card, CardTitle, CardHeader, CardContent } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Skeleton } from "@/components/ui/Skeleton";
import { useNotification } from "@/components/ui/Notification";

import { collectionClient, policyClient } from "@/lib/connect/client";
import { invalidatePolicyVersions } from "@/lib/queries/policyVersions";
import { PolicyHistory } from "@/components/features/policy/PolicyHistory";
import type { PolicyDiagnostic } from "@/gen/paladin/admin/v1/policy_service_pb";
import { cn } from "@/lib/utils";
import { validateNotice } from "@/lib/policyValidation";

import { useCollection } from "../collection-context";
import { errorMessage } from "@/hooks/errorContract";

export default function CollectionPolicyPage() {
  const changesBlocked = useTenantChangesBlocked();
  const { collection, setCollection } = useCollection();
  const { showNotification } = useNotification();
  const qc = useQueryClient();

  const [policyText, setPolicyText] = useState(collection.cedarPolicy);
  const [validating, setValidating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [diagnostics, setDiagnostics] = useState<PolicyDiagnostic[] | null>(
    null,
  );

  // Pull fresh editor state when the context Collection changes (another
  // session bumped the resource and we refetched). Render-phase adjust-on-
  // change (not set-state-in-effect) — `collection` identity is stable between
  // renders via TanStack structural sharing. Diagnostics clear on re-seed.
  const [seededCollection, setSeededCollection] = useState(collection);
  if (collection !== seededCollection) {
    setSeededCollection(collection);
    setPolicyText(collection.cedarPolicy);
    setDiagnostics(null);
  }

  const handleValidate = useCallback(async () => {
    setValidating(true);
    try {
      const res = await policyClient.validate({ cedarPolicy: policyText });
      setDiagnostics(res.diagnostics);
      const notice = validateNotice(res);
      if (notice) showNotification(notice);
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

  // Confirmed beside the Save button, where it was pressed.
  const { shown: saved, confirm: confirmSaved } = useConfirmation();
  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      const updated = await collectionClient.setCollectionPolicy({
        name: collection.name,
        resourceVersion: collection.resourceVersion,
        cedarPolicy: policyText,
      });
      setCollection(updated);
      // The save is the layer's newest version.
      void invalidatePolicyVersions(qc, collection.name);
      confirmSaved();
    } catch (err) {
      const msg = errorMessage(err, "Save failed");
      showNotification({ type: "error", title: "Save failed", message: msg });
    } finally {
      setSaving(false);
    }
  }, [
    collection,
    policyText,
    setCollection,
    showNotification,
    confirmSaved,
    qc,
  ]);

  return (
    <Card>
      <CardHeader className="flex items-center justify-between">
        <CardTitle className="text-sm font-semibold" icon={ShieldCheckIcon}>
          Cedar policy{" "}
          <Link
            href="/policies"
            className="ml-2 text-xs font-normal text-muted-foreground hover:text-primary hover:underline"
          >
            open in editor →
          </Link>
        </CardTitle>
        <div className="flex gap-2">
          <Confirmation shown={saved}>Saved</Confirmation>
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
            disabled={Boolean(changesBlocked) || saving}
            title={changesBlocked ?? undefined}
          >
            {saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {collection.resourceVersion === "" ? (
          <Skeleton className="h-48 w-full" />
        ) : (
          <Textarea
            value={policyText}
            onChange={(e) => setPolicyText(e.target.value)}
            placeholder="// permit ( principal, action, resource );"
            className="min-h-55 font-mono text-xs leading-relaxed"
            spellCheck={false}
          />
        )}
        {diagnostics !== null && diagnostics.length === 0 && (
          <div className="flex items-center gap-2 rounded-md border border-success/40 bg-success/10 p-2 text-sm text-success">
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
                      : "border-warning/40 bg-warning/10 text-warning",
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
        {collection.resourceVersion !== "" && (
          <div className="border-t border-border pt-3">
            <PolicyHistory resourceName={collection.name} draft={policyText} />
          </div>
        )}
      </CardContent>
    </Card>
  );
}
