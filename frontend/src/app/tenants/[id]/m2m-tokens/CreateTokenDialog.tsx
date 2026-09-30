"use client";

import { useCallback, useState } from "react";
import {
  CheckCircleIcon,
  ClipboardDocumentIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { ConnectError } from "@connectrpc/connect";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
  };

  const toggleAudience = (aud: string) => {
    const next = new Set(audience);
    if (next.has(aud)) next.delete(aud);
    else next.add(aud);
    setAudience(next);
  };

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
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
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Create failed";
      showNotification({
        type: "error",
        title: "Create failed",
        message: msg,
      });
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

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-xl">
        {reveal ? (
          <div>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <CheckCircleIcon className="size-5 text-emerald-500" />
                Token created
              </DialogTitle>
              <DialogDescription>
                Copy the secret now — the control plane only stores its argon2id
                hash, so this is the last time it will be visible.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-3 py-4">
              <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
                Save this in a secret manager (Vault, Doppler, K8s Secret). We
                can&apos;t show it again.
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">Token secret</Label>
                <div className="flex gap-2">
                  <Input
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
                <input
                  type="checkbox"
                  checked={revealAcknowledged}
                  onChange={(e) => setRevealAcknowledged(e.target.checked)}
                  className="size-4 accent-primary"
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
          </div>
        ) : (
          <form onSubmit={handleCreate}>
            <DialogHeader>
              <DialogTitle>New M2M token</DialogTitle>
              <DialogDescription>
                Issued for service-to-service automation. Plaintext is returned
                exactly once.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-1.5">
                <Label htmlFor="m2m-name">Name</Label>
                <Input
                  id="m2m-name"
                  autoFocus
                  placeholder="ci-uploader, terraform-svc, …"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
                <p className={T.hint}>
                  Operator-facing label. Shown in audit logs.
                </p>
              </div>

              <div className="space-y-1.5">
                <Label className="text-xs">Audience (planes)</Label>
                <div className="flex flex-wrap gap-2">
                  {AUDIENCE_CHOICES.map((aud) => (
                    <label
                      key={aud}
                      className={cn(
                        "flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 text-xs",
                        audience.has(aud)
                          ? "border-primary bg-primary/10"
                          : "border-input",
                      )}
                    >
                      <input
                        type="checkbox"
                        checked={audience.has(aud)}
                        onChange={() => toggleAudience(aud)}
                        className="size-3 accent-primary"
                      />
                      <span className="font-mono">{aud}</span>
                    </label>
                  ))}
                </div>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="m2m-scopes">Scopes (optional)</Label>
                <Input
                  id="m2m-scopes"
                  placeholder="bucket:photos, object_key:photos/avatar.png, *"
                  value={scopes}
                  onChange={(e) => setScopes(e.target.value)}
                  aria-invalid={hasInvalidScopes || undefined}
                />
                <p className={T.hint}>
                  Comma-, space-, or newline-separated. Valid forms:{" "}
                  <code className="font-mono">tenant:&lt;id&gt;</code>,{" "}
                  <code className="font-mono">backend:&lt;id&gt;</code>,{" "}
                  <code className="font-mono">bucket:&lt;name&gt;</code>,{" "}
                  <code className="font-mono">
                    object_key:&lt;bucket&gt;/&lt;key&gt;
                  </code>
                  , or <code className="font-mono">*</code>.
                </p>
                <p className={T.hint}>
                  Leave empty for full tenant access; add scopes to confine the
                  token to specific resources.
                </p>
                {hasInvalidScopes && (
                  <p className="text-xs text-destructive">
                    Invalid scope{invalidScopes.length > 1 ? "s" : ""}:{" "}
                    <span className="font-mono">
                      {invalidScopes.join(", ")}
                    </span>
                    . Each scope must match one of the forms above.
                  </p>
                )}
              </div>

              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="m2m-rpm">Rate limit (rpm)</Label>
                  <Input
                    id="m2m-rpm"
                    type="number"
                    min={0}
                    placeholder="0 = unlimited"
                    value={rateLimit}
                    onChange={(e) => setRateLimit(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="m2m-ttl">Expiration</Label>
                  <Select
                    id="m2m-ttl"
                    options={TTL_OPTIONS.map((o) => ({
                      value: o.value,
                      label: o.label,
                    }))}
                    value={ttl}
                    onChange={setTtl}
                    className="w-full"
                  />
                </div>
              </div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => handleClose(false)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={
                  creating ||
                  !name.trim() ||
                  audience.size === 0 ||
                  !tenantId ||
                  hasInvalidScopes
                }
              >
                {creating ? "Creating…" : "Create token"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
