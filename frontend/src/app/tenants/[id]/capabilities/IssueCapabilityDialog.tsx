"use client";

import React, { useCallback, useState } from "react";
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
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/Select";
import { ChipInput } from "@/components/ui/ChipInput";
import { Checkbox } from "@/components/ui/checkbox";
import {
  FormDialog,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { useNotification } from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import { copyToClipboard, cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { ALLOWED_UNIT_CODES, parseMicros } from "@/lib/format/money";
import {
  PrincipalKind,
  type Capability,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import { ToggleRow, LimitInput } from "./_form-fields";
import {
  TTL_OPTIONS,
  OP_CHOICES,
  AUDIENCE_CHOICES,
  PRINCIPAL_KIND_OPTIONS,
} from "./_constants";
import { errorMessage } from "@/hooks/errorContract";

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
  const [submitError, setSubmitError] = useState<string | null>(null);

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
    setSubmitError(null);
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

  // Unlimited toggle wins: protocol's 0 = "no cap". With it off the operator
  // must have entered a positive number — held otherwise, so the dialog never
  // silently sends 0 = forbid.
  const maxRequests = issueMaxRequestsUnlimited
    ? 0
    : Number.parseInt(issueMaxRequests, 10);
  // Parsed straight to micros, never through a float: "0.1" is 100000n.
  const maxBudgetMicros = issueMaxBudgetUnlimited
    ? 0n
    : parseMicros(issueMaxBudget);
  const maxRequestsError =
    !issueMaxRequestsUnlimited &&
    issueMaxRequests !== "" &&
    (!Number.isFinite(maxRequests) || maxRequests <= 0)
      ? "A positive number, or Unlimited."
      : null;
  const maxBudgetError =
    !issueMaxBudgetUnlimited &&
    issueMaxBudget !== "" &&
    (maxBudgetMicros === null || maxBudgetMicros <= 0n)
      ? "A positive amount (up to six decimals), or Unlimited."
      : null;

  const blockedReason = !tenantId
    ? "Waiting for the tenant."
    : !issueSubject.trim()
      ? "Enter the subject to continue."
      : issueOps.size === 0
        ? "Allow at least one op."
        : issueAudience.size === 0
          ? "Pick at least one plane."
          : !issueMaxRequestsUnlimited &&
              (maxRequestsError !== null || issueMaxRequests === "")
            ? "Set max requests, or make it Unlimited."
            : !issueMaxBudgetUnlimited &&
                (maxBudgetError !== null || issueMaxBudget === "")
              ? "Set max budget, or make it Unlimited."
              : null;

  const handleIssue = async () => {
    const ttlSeconds = TTL_OPTIONS.find((o) => o.value === issueTtl)?.seconds;
    setSubmitError(null);
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
          resourcePrefixes: issueResourcePrefixes,
          resourceUris: [],
          maxRequests,
          maxBudgetMicros: maxBudgetMicros ?? 0n,
          unitCode: issueUnitCode,
          allowTaintedRead: false,
          idempotencyKeyRequired: false,
          sourceIpCidr: issueSourceCidr,
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
      setSubmitError(errorMessage(err, "Issue failed"));
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

  if (reveal) {
    return (
      <Dialog open={open} onOpenChange={(o) => !o && handleClose()}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <CheckCircleIcon className="size-5 text-success" />
              Capability issued
            </DialogTitle>
            <DialogDescription>
              Only its metadata is stored: this is the last time the token is
              shown.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning">
              <ExclamationTriangleIcon className="mr-1 inline-block size-4 align-text-bottom" />
              Hand it over a secure channel: anyone holding it can act under its
              caveats until it expires.
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cap-token">Capability JWT</Label>
              <div className="flex gap-2">
                <textarea
                  id="cap-token"
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
              <Checkbox
                checked={revealAcknowledged}
                onCheckedChange={(v) => setRevealAcknowledged(v === true)}
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
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) handleClose();
      }}
      title="Issue capability"
      description="A signed token for an agent or service; its caveats are checked on every call."
      width="lg"
      onSubmit={() => void handleIssue()}
      submitLabel="Issue capability"
      submittingLabel="Issuing…"
      submitting={issuing}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection title="Principal">
        <FormRow>
          <FormField label="Kind">
            {(control) => (
              <Select
                id={control.id}
                options={PRINCIPAL_KIND_OPTIONS}
                value={issuePrincipalKind}
                onChange={setIssuePrincipalKind}
              />
            )}
          </FormField>
          <FormField label="Subject" required>
            {(control) => (
              <Input
                {...control}
                autoFocus
                placeholder="agent id, user subject or service account"
                value={issueSubject}
                onChange={(e) => setIssueSubject(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Authorization">
        <fieldset className="space-y-1.5">
          <legend className="text-sm font-medium">Allowed ops</legend>
          <ToggleRow
            options={OP_CHOICES}
            selected={issueOps}
            onToggle={(v) => toggleSetEntry(setIssueOps, issueOps, v)}
          />
        </fieldset>
        <fieldset className="space-y-1.5">
          <legend className="text-sm font-medium">Planes</legend>
          <ToggleRow
            options={AUDIENCE_CHOICES}
            selected={issueAudience}
            onToggle={(v) => toggleSetEntry(setIssueAudience, issueAudience, v)}
          />
        </fieldset>
      </FormSection>

      <FormSection title="Restrictions">
        <FormRow>
          <FormField
            label="Resource prefixes"
            hint="Enter, comma or space adds one. Empty means no restriction."
          >
            {(control) => (
              <ChipInput
                id={control.id}
                values={issueResourcePrefixes}
                onChange={setIssueResourcePrefixes}
                placeholder="objects/contracts/2026/"
              />
            )}
          </FormField>
          <FormField
            label="Source IP ranges"
            hint="Only clients inside one of these CIDRs."
          >
            {(control) => (
              <ChipInput
                id={control.id}
                values={issueSourceCidr}
                onChange={setIssueSourceCidr}
                placeholder="10.0.0.0/8"
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Limits">
        <FormRow>
          <FormField label="Max requests" error={maxRequestsError}>
            {(control) => (
              <LimitInput
                {...control}
                value={issueMaxRequests}
                onChange={setIssueMaxRequests}
                unlimited={issueMaxRequestsUnlimited}
                onUnlimitedChange={setIssueMaxRequestsUnlimited}
                placeholder="e.g. 1000"
              />
            )}
          </FormField>
          <FormField label="Expires after">
            {(control) => (
              <Select
                id={control.id}
                options={TTL_OPTIONS.map((o) => ({
                  value: o.value,
                  label: o.label,
                }))}
                value={issueTtl}
                onChange={setIssueTtl}
              />
            )}
          </FormField>
        </FormRow>
        <FormRow>
          <FormField label="Max budget" error={maxBudgetError}>
            {(control) => (
              <LimitInput
                {...control}
                type="number"
                step="0.01"
                value={issueMaxBudget}
                onChange={setIssueMaxBudget}
                unlimited={issueMaxBudgetUnlimited}
                onUnlimitedChange={setIssueMaxBudgetUnlimited}
                placeholder="e.g. 25.00"
              />
            )}
          </FormField>
          <FormField
            label="Currency / unit"
            hint="UNIT meters without a currency."
          >
            {(control) => (
              <Select
                id={control.id}
                options={ALLOWED_UNIT_CODES.map((u) => ({
                  value: u,
                  label: u,
                }))}
                value={issueUnitCode}
                onChange={setIssueUnitCode}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>
    </FormDialog>
  );
}
