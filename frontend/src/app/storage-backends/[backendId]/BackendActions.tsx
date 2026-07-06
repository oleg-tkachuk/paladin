"use client";

// BackendActions — the management surface for a single storage backend,
// living on the backend detail page. Wraps four admin BackendService RPCs
// that the list/create page deliberately left out (see that page's header):
//
//   • TestBackend        — probe connectivity on demand (read-only diagnostic)
//   • UpdateBackend      — edit mutable metadata (OCC-guarded)
//   • RotateCredentials  — point at a new secret ref with a grace window
//   • DeleteBackend      — remove the backend (force past bucket refs)
//
// Enable / drain / maintenance keep their own toggles on the list page; those
// are per-row bulk-friendly actions, whereas these four are single-backend,
// dialog-driven, and destructive-leaning — hence the detail page.

import { useState } from "react";
import { useRouter } from "next/navigation";
import { ConnectError } from "@connectrpc/connect";
import {
  BoltIcon,
  KeyIcon,
  PencilSquareIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useBackends, UpdateBackendInput } from "@/hooks/useBackends";
import { useNotification } from "@/components/ui/Notification";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import type { TestBackendResponse } from "@/gen/paladin/admin/v1/backend_service_pb";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

function errText(err: unknown): string {
  if (err instanceof ConnectError) return err.rawMessage;
  if (err instanceof Error) return err.message;
  return String(err);
}

export function BackendActions({ backend }: { backend: StorageBackend }) {
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
        message: errText(err),
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
          className={
            testResult.reachable ? "text-emerald-500" : "text-destructive"
          }
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
            showNotification({
              type: "error",
              title: "Could not update backend",
              message: errText(err),
            });
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
            showNotification({
              type: "error",
              title: "Could not rotate credentials",
              message: errText(err),
            });
          }
        }}
      />

      <DeleteBackendDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        backendId={bid}
        onSubmit={async (force) => {
          try {
            await deleteBackend(bid, backend.resourceVersion, force);
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
              message: errText(err),
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
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit backend</DialogTitle>
          <DialogDescription>
            {backend.backendId} · credentials, enable/drain state are managed
            separately.
          </DialogDescription>
        </DialogHeader>
        <EditBackendForm
          backend={backend}
          onCancel={() => onOpenChange(false)}
          onSubmit={onSubmit}
        />
      </DialogContent>
    </Dialog>
  );
}

function EditBackendForm({
  backend,
  onCancel,
  onSubmit,
}: {
  backend: StorageBackend;
  onCancel: () => void;
  onSubmit: (input: UpdateBackendInput) => Promise<void>;
}) {
  const [displayName, setDisplayName] = useState(backend.displayName);
  const [endpoint, setEndpoint] = useState(backend.endpoint);
  const [publicEndpoint, setPublicEndpoint] = useState(backend.publicEndpoint);
  const [region, setRegion] = useState(backend.region);
  const [forcePathStyle, setForcePathStyle] = useState(backend.forcePathStyle);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      await onSubmit({
        displayName,
        endpoint,
        publicEndpoint,
        region,
        forcePathStyle,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="space-y-3">
        <div className="space-y-1">
          <Label htmlFor="edit-display">Display name</Label>
          <Input
            id="edit-display"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="edit-endpoint">Endpoint</Label>
          <Input
            id="edit-endpoint"
            value={endpoint}
            onChange={(e) => setEndpoint(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="edit-public">Public endpoint</Label>
          <Input
            id="edit-public"
            value={publicEndpoint}
            onChange={(e) => setPublicEndpoint(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="edit-region">Region</Label>
          <Input
            id="edit-region"
            value={region}
            onChange={(e) => setRegion(e.target.value)}
          />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={forcePathStyle}
            onCheckedChange={(v) => setForcePathStyle(v === true)}
          />
          Force path-style addressing
        </label>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button onClick={() => void submit()} disabled={busy}>
          {busy ? "Saving…" : "Save changes"}
        </Button>
      </DialogFooter>
    </>
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
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Rotate credentials</DialogTitle>
          <DialogDescription>
            {backendId} · the old secret stays valid until the grace window
            elapses.
          </DialogDescription>
        </DialogHeader>
        <RotateCredentialsForm
          currentRef={currentRef}
          onCancel={() => onOpenChange(false)}
          onSubmit={onSubmit}
        />
      </DialogContent>
    </Dialog>
  );
}

function RotateCredentialsForm({
  currentRef,
  onCancel,
  onSubmit,
}: {
  currentRef: string;
  onCancel: () => void;
  onSubmit: (newSecretRef: string, gracePeriod: string) => Promise<void>;
}) {
  const [newSecretRef, setNewSecretRef] = useState("");
  const [gracePeriod, setGracePeriod] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      await onSubmit(newSecretRef.trim(), gracePeriod.trim());
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="space-y-3">
        {currentRef && (
          <p className="font-mono text-[11px] text-muted-foreground">
            current: {currentRef}
          </p>
        )}
        <div className="space-y-1">
          <Label htmlFor="rot-ref">New secret ref</Label>
          <Input
            id="rot-ref"
            placeholder="vault://kv/paladin/primary"
            value={newSecretRef}
            onChange={(e) => setNewSecretRef(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="rot-grace">Grace period</Label>
          <Input
            id="rot-grace"
            placeholder="30m (blank = server default)"
            value={gracePeriod}
            onChange={(e) => setGracePeriod(e.target.value)}
          />
        </div>
      </div>
      <DialogFooter>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button
          onClick={() => void submit()}
          disabled={busy || newSecretRef.trim() === ""}
        >
          {busy ? "Rotating…" : "Rotate"}
        </Button>
      </DialogFooter>
    </>
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
  onSubmit: (force: boolean) => Promise<void>;
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
  onSubmit: (force: boolean) => Promise<void>;
}) {
  const [force, setForce] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      await onSubmit(force);
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
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={force}
            onCheckedChange={(v) => setForce(v === true)}
          />
          Force — delete even if buckets still reference it
        </label>
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
