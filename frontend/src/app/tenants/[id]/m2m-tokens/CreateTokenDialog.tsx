"use client";

import { useCallback, useState } from "react";
import {
  CheckCircleIcon,
  ClipboardDocumentIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Checkbox } from "@/components/ui/checkbox";
import {
  FormDialog,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { apiTokenClient } from "@/lib/connect/client";
import { copyToClipboard, cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";

import {
  TTL_OPTIONS,
  AUDIENCE_CHOICES,
  parseScopes,
  isValidScope,
} from "./_constants";
import { errorMessage } from "@/hooks/errorContract";

/**
 * Create-M2M-token dialog, extracted from the page. Owns the whole create form
 * + the one-shot reveal panel + the create RPC. Plaintext is returned exactly
 * once, so the dialog refuses to close while a reveal is unacknowledged. The
 * page supplies the tenant, prepends the created token via onCreated, and
 * refetches on close via onClose.
 */
export function CreateTokenDialog({
  open,
  tenantId,
  onClose,
  onCreated,
}: {
  open: boolean;
  tenantId: string;
  onClose: () => void;
  onCreated: (token: APIToken) => void;
}) {
  const { showNotification } = useNotification();

  const [name, setName] = useState("");
  const [scopes, setScopes] = useState("");
  const [audience, setAudience] = useState<Set<string>>(new Set(["data"]));
  const [ttl, setTtl] = useState("90d");
  const [rateLimit, setRateLimit] = useState("0");
  const [creating, setCreating] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // After success: { token, prefix } until the user dismisses.
  const [reveal, setReveal] = useState<{
    token: string;
    prefix: string;
  } | null>(null);
  const [revealAcknowledged, setRevealAcknowledged] = useState(false);

  const resetForm = () => {
    setName("");
    setScopes("");
    setAudience(new Set(["data"]));
    setTtl("90d");
    setRateLimit("0");
    setReveal(null);
    setRevealAcknowledged(false);
    setSubmitError(null);
  };

  const toggleAudience = (aud: string) => {
    const next = new Set(audience);
    if (next.has(aud)) next.delete(aud);
    else next.add(aud);
    setAudience(next);
  };

  const handleCreate = async () => {
    if (!tenantId || !name.trim()) return;
    if (audience.size === 0) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "At least one audience plane must be selected.",
      });
      return;
    }
    const ttlSeconds = TTL_OPTIONS.find((o) => o.value === ttl)?.seconds;
    const scopeList = parseScopes(scopes);
    if (scopeList.some((s) => !isValidScope(s))) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "Fix the invalid scopes before creating the token.",
      });
      return;
    }
    const rpm = Number.parseInt(rateLimit || "0", 10);

    setCreating(true);
    try {
      const res = await apiTokenClient.create({
        parent: `tenants/${tenantId}`,
        displayName: name.trim(),
        ttlSeconds: BigInt(ttlSeconds ?? 0),
        scopes: scopeList,
        audience: Array.from(audience),
        rateLimitRpm: Number.isFinite(rpm) && rpm > 0 ? rpm : 0,
      });
      setReveal({
        token: res.token,
        prefix: res.apiToken?.prefix ?? "",
      });
      if (res.apiToken) {
        onCreated(res.apiToken);
      }
      showNotification({
        type: "success",
        title: "Token created",
        message: "Copy the secret now — it can't be shown again.",
      });
    } catch (err) {
      setSubmitError(errorMessage(err, "Create failed"));
    } finally {
      setCreating(false);
    }
  };

  // Close path: blocked while a reveal is unacknowledged. On a real close we
  // reset the form and hand control back to the page (which refetches).
  const handleClose = (next: boolean) => {
    if (next) return;
    if (reveal && !revealAcknowledged) return;
    resetForm();
    onClose();
  };

  const copyToken = useCallback(async () => {
    if (!reveal) return;
    const ok = await copyToClipboard(reveal.token);
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok
        ? "Token secret copied to clipboard."
        : "Clipboard unavailable.",
    });
  }, [reveal, showNotification]);

  // Live scope validation drives the inline error + submit gate. Mirrors
  // the backend's mint-time rejection so an invalid scope never leaves
  // the browser.
  const invalidScopes = parseScopes(scopes).filter((s) => !isValidScope(s));
  const hasInvalidScopes = invalidScopes.length > 0;

  const blockedReason = !tenantId
    ? "Waiting for the tenant."
    : !name.trim()
      ? "Enter a name to continue."
      : audience.size === 0
        ? "Pick at least one plane."
        : hasInvalidScopes
          ? "Fix the scopes to continue."
          : null;

  if (reveal) {
    return (
      <Dialog open={open} onOpenChange={handleClose}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <CheckCircleIcon className="size-5 text-emerald-500" />
              Token created
            </DialogTitle>
            <DialogDescription>
              Only its hash is stored: this is the last time the secret is
              shown.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
              <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
              Save it in a secret manager (Vault, Doppler, a Kubernetes Secret)
              before closing.
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="m2m-secret">Token secret</Label>
              <div className="flex gap-2">
                <Input
                  id="m2m-secret"
                  readOnly
                  value={reveal.token}
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button variant="outline" onClick={copyToken} type="button">
                  <ClipboardDocumentIcon className="size-4" />
                  Copy
                </Button>
              </div>
              <p className={cn(T.code, "text-muted-foreground")}>
                prefix: paladin_pat_{reveal.prefix}…
              </p>
            </div>
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <Checkbox
                checked={revealAcknowledged}
                onCheckedChange={(v) => setRevealAcknowledged(v === true)}
              />
              <span>I&apos;ve copied this token somewhere safe.</span>
            </label>
          </div>
          <DialogFooter>
            <Button
              type="button"
              onClick={() => handleClose(false)}
              disabled={!revealAcknowledged}
            >
              Done
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={handleClose}
      title="New M2M token"
      description="For service-to-service automation. The secret is shown once."
      width="lg"
      onSubmit={() => void handleCreate()}
      submitLabel="Create token"
      submittingLabel="Creating…"
      submitting={creating}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection title="Token">
        <FormField label="Name" required hint="Shown in audit logs.">
          {(control) => (
            <Input
              {...control}
              autoFocus
              placeholder="ci-uploader, terraform-svc, …"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          )}
        </FormField>
        <FormRow>
          <FormField label="Expiration">
            {(control) => (
              <Select
                id={control.id}
                options={TTL_OPTIONS.map((o) => ({
                  value: o.value,
                  label: o.label,
                }))}
                value={ttl}
                onChange={setTtl}
              />
            )}
          </FormField>
          <FormField label="Rate limit (rpm)" hint="0 is unlimited.">
            {(control) => (
              <Input
                {...control}
                type="number"
                min={0}
                value={rateLimit}
                onChange={(e) => setRateLimit(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Access">
        <fieldset className="space-y-1.5">
          <legend className="text-sm font-medium">Planes</legend>
          <div className="flex flex-wrap gap-2">
            {AUDIENCE_CHOICES.map((aud) => (
              <label
                key={aud}
                className={cn(
                  "flex cursor-pointer items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs",
                  audience.has(aud) ? "border-primary/60 bg-primary/10" : "",
                )}
              >
                <Checkbox
                  checked={audience.has(aud)}
                  onCheckedChange={() => toggleAudience(aud)}
                />
                <span className="font-mono">{aud}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <FormField
          label="Scopes"
          error={
            hasInvalidScopes
              ? `Not a scope: ${invalidScopes.join(", ")}.`
              : null
          }
          hint={
            <>
              Empty gives the whole tenant. Separate with commas, spaces or
              newlines: <code className="font-mono">tenant:&lt;id&gt;</code>,{" "}
              <code className="font-mono">backend:&lt;id&gt;</code>,{" "}
              <code className="font-mono">bucket:&lt;name&gt;</code>,{" "}
              <code className="font-mono">
                object_key:&lt;bucket&gt;/&lt;key&gt;
              </code>{" "}
              or <code className="font-mono">*</code>.
            </>
          }
        >
          {(control) => (
            <Input
              {...control}
              placeholder="bucket:photos, object_key:photos/avatar.png"
              value={scopes}
              onChange={(e) => setScopes(e.target.value)}
            />
          )}
        </FormField>
      </FormSection>
    </FormDialog>
  );
}
