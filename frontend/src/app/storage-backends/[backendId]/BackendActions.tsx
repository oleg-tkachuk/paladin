"use client";

// BackendActions — the management surface for a single storage backend,
// living on the backend detail page. Wraps four admin BackendService RPCs
// that the list/create page deliberately left out (see that page's header):
//
//   • TestBackend        — probe connectivity and S3 features on demand
//   • UpdateBackend      — edit mutable metadata (OCC-guarded)
//   • RotateCredentials  — point at a new secret ref with a grace window
//   • DeleteBackend      — remove the backend (force past bucket refs)
//
// Enable / drain / maintenance keep their own toggles on the list page; those
// are per-row bulk-friendly actions, whereas these four are single-backend,
// dialog-driven, and destructive-leaning — hence the detail page.

import { useState } from "react";
import { useRouter } from "next/navigation";
import {
  BoltIcon,
  KeyIcon,
  PencilSquareIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useBackends, UpdateBackendInput } from "@/hooks/useBackends";
import { useNotification } from "@/components/ui/Notification";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { EventTarget, SseType } from "@/gen/paladin/admin/v1/types_pb";
import type { Duration } from "@bufbuild/protobuf/wkt";
import type { TestBackendResponse } from "@/gen/paladin/admin/v1/backend_service_pb";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import {
  FormDialog,
  FormDisclosure,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { Select } from "@/components/ui/Select";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { errorMessage } from "@/hooks/errorContract";

export function BackendActions({
  backend,
  onTested,
}: {
  backend: StorageBackend;
  /** Called after a probe, which records the backend's S3 features. */
  onTested?: () => void;
}) {
  const router = useRouter();
  const { showNotification } = useNotification();
  // autoFetch=false: this component only needs the mutations, not the list —
  // fetching it here would fire a redundant ListBackends on every mount.
  const { updateBackend, rotateCredentials, testBackend, deleteBackend } =
    useBackends(false);

  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<TestBackendResponse | null>(
    null,
  );

  const [editOpen, setEditOpen] = useState(false);
  const [rotateOpen, setRotateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const bid = backend.backendId;

  const runTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const res = await testBackend(bid);
      setTestResult(res);
      onTested?.();
      showNotification({
        type: res.reachable ? "success" : "error",
        title: res.reachable ? "Backend reachable" : "Backend unreachable",
        message: res.reachable
          ? `${bid} · ${res.latencyMs}ms`
          : res.errorMessage || bid,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Probe failed",
        message: errorMessage(err),
      });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        variant="outline"
        size="sm"
        onClick={() => void runTest()}
        disabled={testing}
      >
        <BoltIcon className="size-4" />
        {testing ? "Testing…" : "Test connectivity"}
      </Button>
      {testResult && (
        <Badge
          variant="outline"
          className={testResult.reachable ? "text-success" : "text-destructive"}
        >
          {testResult.reachable
            ? `reachable · ${testResult.latencyMs}ms`
            : `unreachable${testResult.errorMessage ? `: ${testResult.errorMessage}` : ""}`}
        </Badge>
      )}

      <Button variant="outline" size="sm" onClick={() => setEditOpen(true)}>
        <PencilSquareIcon className="size-4" />
        Edit
      </Button>
      <Button variant="outline" size="sm" onClick={() => setRotateOpen(true)}>
        <KeyIcon className="size-4" />
        Rotate credentials
      </Button>
      <Button
        variant="outline"
        size="sm"
        className="text-destructive"
        onClick={() => setDeleteOpen(true)}
      >
        <TrashIcon className="size-4" />
        Delete
      </Button>

      <EditBackendDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        backend={backend}
        onSubmit={async (input) => {
          try {
            await updateBackend(bid, backend.resourceVersion, input);
            showNotification({
              type: "success",
              title: "Backend updated",
              message: bid,
            });
            setEditOpen(false);
          } catch (err) {
            // Shown in the dialog, which stays open to correct and retry.
            throw new Error(errorMessage(err));
          }
        }}
      />

      <RotateCredentialsDialog
        open={rotateOpen}
        onOpenChange={setRotateOpen}
        backendId={bid}
        currentRef={backend.credentialsSecretRef}
        onSubmit={async (newSecretRef, gracePeriod) => {
          try {
            await rotateCredentials(bid, newSecretRef, gracePeriod);
            showNotification({
              type: "success",
              title: "Credentials rotated",
              message: bid,
            });
            setRotateOpen(false);
          } catch (err) {
            throw new Error(errorMessage(err));
          }
        }}
      />

      <DeleteBackendDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        backendId={bid}
        onSubmit={async () => {
          try {
            await deleteBackend(bid, backend.resourceVersion);
            showNotification({
              type: "success",
              title: "Backend deleted",
              message: bid,
            });
            router.push("/storage-backends");
          } catch (err) {
            showNotification({
              type: "error",
              title: "Could not delete backend",
              message: errorMessage(err),
            });
          }
        }}
      />
    </div>
  );
}

// Each dialog splits into a thin <Dialog> shell + an inner *Form. The form
// holds the field state and is a child of DialogContent, which Radix mounts
// only while open — so every reopen mounts a fresh form seeded from current
// props, with no reset-on-open effect (and thus no set-state-in-effect).
function EditBackendDialog({
  open,
  onOpenChange,
  backend,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  backend: StorageBackend;
  onSubmit: (input: UpdateBackendInput) => Promise<void>;
}) {
  return open ? (
    <EditBackendForm
      backend={backend}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  ) : null;
}

// Duration ⇄ milliseconds. The form edits a single integer because "poll every
// N ms" is how the operator thinks about it; the wire type is seconds + nanos.
function durationToMs(d: Duration | undefined): number {
  if (!d) return 0;
  return Number(d.seconds) * 1000 + Math.round(d.nanos / 1_000_000);
}

const SSE_OPTIONS = [
  { value: String(SseType.NONE), label: "None" },
  { value: String(SseType.AES256), label: "AES-256 (SSE-S3)" },
  { value: String(SseType.KMS), label: "KMS (SSE-KMS)" },
];

const EVENT_TARGET_OPTIONS = [
  { value: String(EventTarget.NONE), label: "None" },
  { value: String(EventTarget.SQS), label: "SQS" },
  { value: String(EventTarget.REDIS), label: "Redis" },
];

function EditBackendForm({
  backend,
  onOpenChange,
  onSubmit,
}: {
  backend: StorageBackend;
  onOpenChange: (v: boolean) => void;
  onSubmit: (input: UpdateBackendInput) => Promise<void>;
}) {
  const [displayName, setDisplayName] = useState(backend.displayName);
  const [endpoint, setEndpoint] = useState(backend.endpoint);
  const [publicEndpoint, setPublicEndpoint] = useState(backend.publicEndpoint);
  const [region, setRegion] = useState(backend.region);
  const [forcePathStyle, setForcePathStyle] = useState(backend.forcePathStyle);
  const [busy, setBusy] = useState(false);

  // ── advanced: sse / events / cedar_policy ─────────────────────────────
  //
  // The originals are captured once, from the backend this form opened on, and
  // every group is sent only when it differs from them. That is not a
  // performance nicety — the server writes each of these groups wholesale from
  // the message when its mask path is named (see useBackends), so sending an
  // untouched group would rewrite it from whatever the form happened to hold.
  const original = {
    sseType: backend.sse?.type ?? SseType.NONE,
    sseKeyId: backend.sse?.keyId ?? "",
    eventsEnabled: backend.events?.enabled ?? false,
    eventsTarget: backend.events?.target ?? EventTarget.NONE,
    eventsQueueUrl: backend.events?.queueUrl ?? "",
    eventsPollMs: durationToMs(backend.events?.pollInterval),
    cedarPolicy: backend.cedarPolicy,
  };

  const [sseType, setSseType] = useState<SseType>(original.sseType);
  const [sseKeyId, setSseKeyId] = useState(original.sseKeyId);
  const [eventsEnabled, setEventsEnabled] = useState(original.eventsEnabled);
  const [eventsTarget, setEventsTarget] = useState<EventTarget>(
    original.eventsTarget,
  );
  const [eventsQueueUrl, setEventsQueueUrl] = useState(original.eventsQueueUrl);
  const [eventsPollMs, setEventsPollMs] = useState(
    String(original.eventsPollMs),
  );
  const [cedarPolicy, setCedarPolicy] = useState(original.cedarPolicy);

  const pollMs = Number(eventsPollMs);
  const pollMsValid = Number.isInteger(pollMs) && pollMs >= 0;
  // The server rejects KMS without a key id; saying so here beats a round trip
  // that comes back InvalidArgument.
  const kmsNeedsKey = sseType === SseType.KMS && sseKeyId.trim() === "";

  const sseDirty =
    sseType !== original.sseType || sseKeyId !== original.sseKeyId;
  const eventsDirty =
    eventsEnabled !== original.eventsEnabled ||
    eventsTarget !== original.eventsTarget ||
    eventsQueueUrl !== original.eventsQueueUrl ||
    pollMs !== original.eventsPollMs;
  const cedarDirty = cedarPolicy !== original.cedarPolicy;

  const [submitError, setSubmitError] = useState<string | null>(null);
  const submit = async () => {
    setSubmitError(null);
    setBusy(true);
    try {
      await onSubmit({
        displayName,
        endpoint,
        publicEndpoint,
        region,
        forcePathStyle,
        ...(sseDirty && { sse: { type: sseType, keyId: sseKeyId } }),
        ...(eventsDirty && {
          events: {
            enabled: eventsEnabled,
            target: eventsTarget,
            queueUrl: eventsQueueUrl,
            pollIntervalMs: pollMs,
          },
        }),
        ...(cedarDirty && { cedarPolicy }),
      });
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const blockedReason = kmsNeedsKey
    ? "KMS needs a key id."
    : !pollMsValid
      ? "Fix the poll interval."
      : !endpoint.trim()
        ? "The endpoint cannot be empty."
        : null;

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title="Edit backend"
      description={`${backend.backendId} · credentials and the enable and drain state are changed separately.`}
      width="lg"
      onSubmit={() => void submit()}
      submitLabel="Save changes"
      submittingLabel="Saving…"
      submitting={busy}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection title="Connection">
        <FormField label="Display name">
          {(control) => (
            <Input
              {...control}
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          )}
        </FormField>
        <FormRow>
          <FormField label="Endpoint" required>
            {(control) => (
              <Input
                {...control}
                className="font-mono text-xs"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
              />
            )}
          </FormField>
          <FormField label="Public endpoint" hint="Used in presigned URLs.">
            {(control) => (
              <Input
                {...control}
                className="font-mono text-xs"
                value={publicEndpoint}
                onChange={(e) => setPublicEndpoint(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
        <FormRow>
          <FormField label="Region">
            {(control) => (
              <Input
                {...control}
                value={region}
                onChange={(e) => setRegion(e.target.value)}
              />
            )}
          </FormField>
          <label className="flex items-center gap-2 self-end pb-2 text-sm">
            <Checkbox
              checked={forcePathStyle}
              onCheckedChange={(v) => setForcePathStyle(v === true)}
            />
            Force path-style addressing
          </label>
        </FormRow>
      </FormSection>

      {/* In the same dialog rather than a panel of its own: these are fields
          of the same resource under the same OCC token, so a separate surface
          would mean a second write that has to re-read resource_version. */}
      <FormDisclosure title="Advanced — encryption, storage events, Cedar policy">
        <FormRow>
          <FormField label="Server-side encryption">
            {(control) => (
              <Select
                id={control.id}
                options={SSE_OPTIONS}
                value={String(sseType)}
                onChange={(v) => setSseType(Number(v) as SseType)}
              />
            )}
          </FormField>
          {sseType === SseType.KMS ? (
            <FormField
              label="KMS key id"
              required
              error={kmsNeedsKey ? "Required when the type is KMS." : null}
            >
              {(control) => (
                <Input
                  {...control}
                  value={sseKeyId}
                  onChange={(e) => setSseKeyId(e.target.value)}
                />
              )}
            </FormField>
          ) : null}
        </FormRow>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={eventsEnabled}
            onCheckedChange={(v) => setEventsEnabled(v === true)}
          />
          Ingest events from this backend
        </label>
        <FormRow>
          <FormField label="Events target">
            {(control) => (
              <Select
                id={control.id}
                options={EVENT_TARGET_OPTIONS}
                value={String(eventsTarget)}
                onChange={(v) => setEventsTarget(Number(v) as EventTarget)}
              />
            )}
          </FormField>
          <FormField
            label="Poll interval (ms)"
            error={pollMsValid ? null : "Whole milliseconds, zero or more."}
          >
            {(control) => (
              <Input
                {...control}
                inputMode="numeric"
                value={eventsPollMs}
                onChange={(e) => setEventsPollMs(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
        <FormField label="Queue URL">
          {(control) => (
            <Input
              {...control}
              className="font-mono text-xs"
              value={eventsQueueUrl}
              onChange={(e) => setEventsQueueUrl(e.target.value)}
            />
          )}
        </FormField>
        <FormField
          label="Cedar policy"
          hint="Attached to the backend itself — typically a forbid rule pinning which tenants may bind buckets here."
        >
          {(control) => (
            <Textarea
              {...control}
              rows={6}
              className="font-mono text-xs"
              value={cedarPolicy}
              onChange={(e) => setCedarPolicy(e.target.value)}
            />
          )}
        </FormField>
      </FormDisclosure>
    </FormDialog>
  );
}

function RotateCredentialsDialog({
  open,
  onOpenChange,
  backendId,
  currentRef,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  backendId: string;
  currentRef: string;
  onSubmit: (newSecretRef: string, gracePeriod: string) => Promise<void>;
}) {
  // Mounted only while open, so every open starts from an empty form.
  return open ? (
    <RotateCredentialsForm
      backendId={backendId}
      currentRef={currentRef}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  ) : null;
}

function RotateCredentialsForm({
  backendId,
  currentRef,
  onOpenChange,
  onSubmit,
}: {
  backendId: string;
  currentRef: string;
  onOpenChange: (v: boolean) => void;
  onSubmit: (newSecretRef: string, gracePeriod: string) => Promise<void>;
}) {
  const [newSecretRef, setNewSecretRef] = useState("");
  const [gracePeriod, setGracePeriod] = useState("");
  const [busy, setBusy] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const submit = async () => {
    setSubmitError(null);
    setBusy(true);
    try {
      await onSubmit(newSecretRef.trim(), gracePeriod.trim());
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title="Rotate credentials"
      description={`${backendId} · the old secret stays valid until the grace period ends.`}
      onSubmit={() => void submit()}
      submitLabel="Rotate"
      submittingLabel="Rotating…"
      submitting={busy}
      blockedReason={
        newSecretRef.trim() === "" ? "Enter the new secret reference." : null
      }
      error={submitError}
    >
      <FormSection>
        {currentRef ? (
          <p className="font-mono text-caption text-muted-foreground">
            current: {currentRef}
          </p>
        ) : null}
        <FormField label="New secret reference" required>
          {(control) => (
            <Input
              {...control}
              autoFocus
              placeholder="vault://kv/paladin/primary"
              className="font-mono text-xs"
              value={newSecretRef}
              onChange={(e) => setNewSecretRef(e.target.value)}
            />
          )}
        </FormField>
        <FormField label="Grace period" hint="Empty uses the server default.">
          {(control) => (
            <Input
              {...control}
              placeholder="30m"
              value={gracePeriod}
              onChange={(e) => setGracePeriod(e.target.value)}
            />
          )}
        </FormField>
      </FormSection>
    </FormDialog>
  );
}

function DeleteBackendDialog({
  open,
  onOpenChange,
  backendId,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  backendId: string;
  onSubmit: () => Promise<void>;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete backend</DialogTitle>
          <DialogDescription>
            This removes <span className="font-mono">{backendId}</span>. Type
            the id to confirm.
          </DialogDescription>
        </DialogHeader>
        <DeleteBackendForm
          backendId={backendId}
          onCancel={() => onOpenChange(false)}
          onSubmit={onSubmit}
        />
      </DialogContent>
    </Dialog>
  );
}

function DeleteBackendForm({
  backendId,
  onCancel,
  onSubmit,
}: {
  backendId: string;
  onCancel: () => void;
  onSubmit: () => Promise<void>;
}) {
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      await onSubmit();
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="space-y-3">
        <Input
          aria-label="confirm backend id"
          placeholder={backendId}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        <p className="text-xs text-muted-foreground">
          A backend that still has buckets cannot be deleted — delete the
          buckets first. There is no override: the constraint is a foreign key.
        </p>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button
          variant="destructive"
          onClick={() => void submit()}
          disabled={busy || confirm !== backendId}
        >
          {busy ? "Deleting…" : "Delete backend"}
        </Button>
      </DialogFooter>
    </>
  );
}
