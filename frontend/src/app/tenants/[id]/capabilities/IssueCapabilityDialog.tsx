"use client";

import React, { useCallback, useState } from "react";
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
import { ChipInput } from "@/components/ui/ChipInput";
import { useNotification } from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import { copyToClipboard, cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { ALLOWED_UNIT_CODES } from "@/lib/format/money";
import {
  PrincipalKind,
  type Capability,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import { FormSection, Field, ToggleRow, LimitInput } from "./_form-fields";
import {
  TTL_OPTIONS,
  OP_CHOICES,
  AUDIENCE_CHOICES,
  PRINCIPAL_KIND_OPTIONS,
} from "./_constants";

interface IssueCapabilityDialogProps {
  open: boolean;
  tenantId: string;
  // Called when the dialog is dismissed (after the reveal panel is
  // acknowledged, if shown). The page closes it + refetches the list.
  onClose: () => void;
  // Called once a capability is successfully issued so the page can sync its
  // browse filter (principal kind + subject) and seed the list with the new
  // row — List is principal-scoped, so this keeps the just-minted capability
  // visible after the reveal panel closes.
  onIssued: (
    capability: Capability,
    principalKind: string,
    subject: string,
  ) => void;
}

/**
 * Issue-capability dialog, extracted from the capabilities page. Owns the whole
 * issue form (principal / authorization / restrictions / limits), the
 * one-shot token-reveal panel, and the issue RPC. The page-level coupling
 * (refresh the browse list, persist last-browsed principal) is delegated via
 * onIssued; tenants page.test.tsx pins open / validation / submit / success.
 */
export function IssueCapabilityDialog({
  open,
  tenantId,
  onClose,
  onIssued,
}: IssueCapabilityDialogProps) {
  const { showNotification } = useNotification();

  const [issueSubject, setIssueSubject] = useState("");
  const [issuePrincipalKind, setIssuePrincipalKind] = useState<string>(
    String(PrincipalKind.AGENT),
  );
  const [issueOps, setIssueOps] = useState<Set<string>>(
    new Set(["get", "list"]),
  );
  const [issueAudience, setIssueAudience] = useState<Set<string>>(
    new Set(["data"]),
  );
  const [issueResourcePrefixes, setIssueResourcePrefixes] = useState<string[]>(
    [],
  );
  const [issueSourceCidr, setIssueSourceCidr] = useState<string[]>([]);
  const [issueMaxRequests, setIssueMaxRequests] = useState("");
  const [issueMaxRequestsUnlimited, setIssueMaxRequestsUnlimited] =
    useState(true);
  const [issueMaxBudget, setIssueMaxBudget] = useState("");
  const [issueMaxBudgetUnlimited, setIssueMaxBudgetUnlimited] = useState(true);
  const [issueUnitCode, setIssueUnitCode] = useState<string>("UNIT");
  const [issueTtl, setIssueTtl] = useState("1h");
  const [issuing, setIssuing] = useState(false);

  // After success: { token, capability } reveal panel — one-shot view.
  const [reveal, setReveal] = useState<{
    token: string;
    capabilityId: string;
  } | null>(null);
  const [revealAcknowledged, setRevealAcknowledged] = useState(false);

  const resetIssueForm = () => {
    setIssueSubject("");
    setIssuePrincipalKind(String(PrincipalKind.AGENT));
    setIssueOps(new Set(["get", "list"]));
    setIssueAudience(new Set(["data"]));
    setIssueResourcePrefixes([]);
    setIssueSourceCidr([]);
    setIssueMaxRequests("");
    setIssueMaxRequestsUnlimited(true);
    setIssueMaxBudget("");
    setIssueMaxBudgetUnlimited(true);
    setIssueUnitCode("UNIT");
    setIssueTtl("1h");
    setReveal(null);
    setRevealAcknowledged(false);
  };

  const toggleSetEntry = (
    setter: (next: Set<string>) => void,
    current: Set<string>,
    value: string,
  ) => {
    const next = new Set(current);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    setter(next);
  };

  const handleIssue = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!tenantId || !issueSubject.trim()) return;
    if (issueOps.size === 0 || issueAudience.size === 0) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "At least one op and one audience are required.",
      });
      return;
    }
    const ttlSeconds = TTL_OPTIONS.find((o) => o.value === issueTtl)?.seconds;
    const prefixes = issueResourcePrefixes;
    const cidrs = issueSourceCidr;
    // Unlimited toggle wins: protocol's 0 = "no cap". When the toggle is off
    // the user must have entered a positive number — block submit otherwise so
    // the dialog never silently sends 0 = forbid.
    const maxRequests = issueMaxRequestsUnlimited
      ? 0
      : Number.parseInt(issueMaxRequests, 10);
    const maxBudget = issueMaxBudgetUnlimited
      ? 0
      : Number.parseFloat(issueMaxBudget);
    if (
      !issueMaxRequestsUnlimited &&
      (!Number.isFinite(maxRequests) || maxRequests <= 0)
    ) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "Max requests must be a positive number, or toggle Unlimited.",
      });
      return;
    }
    if (
      !issueMaxBudgetUnlimited &&
      (!Number.isFinite(maxBudget) || maxBudget <= 0)
    ) {
      showNotification({
        type: "error",
        title: "Validation",
        message: "Max budget must be a positive number, or toggle Unlimited.",
      });
      return;
    }

    setIssuing(true);
    try {
      const res = await capabilityClient.issue({
        subject: {
          kind: Number(issuePrincipalKind),
          tenantId,
          subject: issueSubject.trim(),
        },
        audience: Array.from(issueAudience),
        caveats: {
          ops: Array.from(issueOps),
          resourcePrefixes: prefixes,
          resourceUris: [],
          maxRequests,
          maxBudgetAmount: maxBudget,
          unitCode: issueUnitCode,
          allowTaintedRead: false,
          idempotencyKeyRequired: false,
          sourceIpCidr: cidrs,
        },
        ttlSeconds: BigInt(ttlSeconds ?? 0),
      });
      setReveal({
        token: res.token,
        capabilityId: res.capability?.id ?? "",
      });
      // Let the page sync its browse filter to the just-issued (kind, subject)
      // and seed the list so the capability is visible after the reveal closes.
      if (res.capability) {
        onIssued(res.capability, issuePrincipalKind, issueSubject.trim());
      }
      showNotification({
        type: "success",
        title: "Capability issued",
        message: "Copy the token now — it can't be shown again.",
      });
    } catch (err) {
      const msg = err instanceof ConnectError ? err.rawMessage : "Issue failed";
      showNotification({ type: "error", title: "Issue failed", message: msg });
    } finally {
      setIssuing(false);
    }
  };

  // Dismiss: while the reveal panel is up, block close until the operator
  // confirms they've handed off the token. Then reset + tell the page.
  const handleClose = () => {
    if (reveal && !revealAcknowledged) return;
    resetIssueForm();
    onClose();
  };

  const copyToken = useCallback(async () => {
    if (!reveal) return;
    const ok = await copyToClipboard(reveal.token);
    showNotification({
      type: ok ? "success" : "error",
      title: ok ? "Copied" : "Copy failed",
      message: ok
        ? "Capability token copied to clipboard."
        : "Clipboard unavailable.",
    });
  }, [reveal, showNotification]);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && handleClose()}>
      <DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-5xl">
        {reveal ? (
          <div>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <CheckCircleIcon className="size-5 text-emerald-500" />
                Capability issued
              </DialogTitle>
              <DialogDescription>
                Copy the JWT now — the control plane only stores its metadata,
                the compact token is shown exactly once.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-3 py-4">
              <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
                <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
                Hand this to the agent over a secure channel — anyone with the
                JWT can act under the caveats until expiry.
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">Capability JWT</Label>
                <div className="flex gap-2">
                  <textarea
                    readOnly
                    value={reveal.token}
                    className="flex-1 rounded-md border border-input bg-background px-2 py-2 font-mono text-xs"
                    rows={4}
                    onFocus={(e) => e.currentTarget.select()}
                  />
                  <Button variant="outline" onClick={copyToken} type="button">
                    <ClipboardDocumentIcon className="size-4" />
                    Copy
                  </Button>
                </div>
                <p className={cn(T.code, "text-muted-foreground")}>
                  id: {reveal.capabilityId}
                </p>
              </div>
              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={revealAcknowledged}
                  onChange={(e) => setRevealAcknowledged(e.target.checked)}
                  className="size-4 accent-primary"
                />
                <span>I&apos;ve handed this token to the agent.</span>
              </label>
            </div>
            <DialogFooter>
              <Button
                type="button"
                onClick={handleClose}
                disabled={!revealAcknowledged}
              >
                Done
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={handleIssue}>
            <DialogHeader>
              <DialogTitle>Issue capability</DialogTitle>
              <DialogDescription>
                Mint a signed JWT for an agent or service. Caveats are enforced
                server-side on every RPC.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              <FormSection title="Principal">
                <div className="grid grid-cols-1 gap-3 md:grid-cols-[140px_1fr]">
                  <Field label="Kind">
                    <Select
                      options={PRINCIPAL_KIND_OPTIONS}
                      value={issuePrincipalKind}
                      onChange={setIssuePrincipalKind}
                      className="w-full"
                    />
                  </Field>
                  <Field label="Subject" htmlFor="cap-issue-subj">
                    <Input
                      id="cap-issue-subj"
                      autoFocus
                      placeholder="agent-id / user subject / service-account name"
                      value={issueSubject}
                      onChange={(e) => setIssueSubject(e.target.value)}
                    />
                  </Field>
                </div>
              </FormSection>

              <FormSection title="Authorization">
                <div className="grid grid-cols-1 gap-3 md:grid-cols-[3fr_2fr]">
                  <Field label="Allowed ops" hint="At least one required.">
                    <ToggleRow
                      options={OP_CHOICES}
                      selected={issueOps}
                      onToggle={(v) => toggleSetEntry(setIssueOps, issueOps, v)}
                    />
                  </Field>
                  <Field
                    label="Audience (planes)"
                    hint="Which Paladin planes accept this token."
                  >
                    <ToggleRow
                      options={AUDIENCE_CHOICES}
                      selected={issueAudience}
                      onToggle={(v) =>
                        toggleSetEntry(setIssueAudience, issueAudience, v)
                      }
                    />
                  </Field>
                </div>
              </FormSection>

              <FormSection title="Restrictions">
                <div className="grid grid-cols-1 gap-3 md:grid-cols-[2fr_1fr]">
                  <Field
                    label="Resource prefixes"
                    optional
                    htmlFor="cap-prefix"
                    hint="Enter / comma / space to add. Empty = no restriction."
                  >
                    <ChipInput
                      id="cap-prefix"
                      values={issueResourcePrefixes}
                      onChange={setIssueResourcePrefixes}
                      placeholder="objects/contracts/2026/"
                    />
                  </Field>
                  <Field
                    label="Source IP CIDR"
                    optional
                    htmlFor="cap-cidr"
                    hint="Restrict to clients whose IP is in one of these ranges."
                  >
                    <ChipInput
                      id="cap-cidr"
                      values={issueSourceCidr}
                      onChange={setIssueSourceCidr}
                      placeholder="10.0.0.0/8"
                    />
                  </Field>
                </div>
              </FormSection>

              <FormSection title="Limits">
                <div className="grid grid-cols-1 gap-3 md:grid-cols-4">
                  <Field label="Max requests" htmlFor="cap-maxreq">
                    <LimitInput
                      id="cap-maxreq"
                      value={issueMaxRequests}
                      onChange={setIssueMaxRequests}
                      unlimited={issueMaxRequestsUnlimited}
                      onUnlimitedChange={setIssueMaxRequestsUnlimited}
                      placeholder="e.g. 1000"
                    />
                  </Field>
                  <Field label="Max budget" htmlFor="cap-maxbudget">
                    <LimitInput
                      id="cap-maxbudget"
                      type="number"
                      step="0.01"
                      value={issueMaxBudget}
                      onChange={setIssueMaxBudget}
                      unlimited={issueMaxBudgetUnlimited}
                      onUnlimitedChange={setIssueMaxBudgetUnlimited}
                      placeholder="e.g. 25.00"
                    />
                  </Field>
                  <Field
                    label="Currency / Unit"
                    hint="USD/EUR/UAH/GBP or UNIT (non-currency metering)."
                  >
                    <Select
                      options={ALLOWED_UNIT_CODES.map((u) => ({
                        value: u,
                        label: u,
                      }))}
                      value={issueUnitCode}
                      onChange={setIssueUnitCode}
                      className="w-full"
                    />
                  </Field>
                  <Field label="TTL">
                    <Select
                      options={TTL_OPTIONS.map((o) => ({
                        value: o.value,
                        label: o.label,
                      }))}
                      value={issueTtl}
                      onChange={setIssueTtl}
                      className="w-full"
                    />
                  </Field>
                </div>
              </FormSection>
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={
                  issuing ||
                  !issueSubject.trim() ||
                  issueOps.size === 0 ||
                  issueAudience.size === 0 ||
                  !tenantId
                }
              >
                {issuing ? "Issuing…" : "Issue capability"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
