"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNotification } from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import { capabilityLeaseQuery } from "@/lib/queries/details";
import { errorMessage } from "@/hooks/errorContract";
import {
  EMPTY_LEASE_FORM,
  type LeaseForm,
  leaseRequestOf,
} from "@/lib/capabilityLease";

/** What the lease form's fields are, in the order shown. */
const FIELDS: readonly {
  key: keyof LeaseForm;
  label: (unit: string) => string;
  placeholder: string;
}[] = [
  {
    key: "amount",
    label: (unit) => `Budget (${unit})`,
    placeholder: "e.g. 10.00",
  },
  { key: "requests", label: () => "Requests", placeholder: "e.g. 100" },
  {
    key: "ttlMinutes",
    label: () => "Lasts (minutes)",
    placeholder: "server default",
  },
];

/**
 * Leases budget and requests of a capability's chain to it, or adds to the
 * lease it holds: its charges then stop at the lease instead of locking
 * every ancestor and the tenant. What it holds counts against them until
 * it is settled.
 */
export function LeaseCapabilityDialog({
  id,
  unit,
  onClose,
}: {
  /** The capability to lease for; null keeps the dialog closed. */
  id: string | null;
  /** The capability's budget unit, which the amount is in. */
  unit: string;
  onClose: () => void;
}) {
  const { showNotification } = useNotification();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<LeaseForm>(EMPTY_LEASE_FORM);
  const parsed = id ? leaseRequestOf(id, unit, form) : null;

  const handleClose = () => {
    setForm(EMPTY_LEASE_FORM);
    onClose();
  };
  const handleLease = async () => {
    if (!id || !parsed || parsed.error !== undefined) return;
    try {
      await capabilityClient.lease(parsed.request);
      showNotification({
        type: "success",
        title: "Budget leased",
        message: `Capability ${id} holds the lease.`,
      });
      void queryClient.invalidateQueries({
        queryKey: capabilityLeaseQuery(id).queryKey,
      });
      handleClose();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Lease failed",
        message: errorMessage(err, "Nothing was leased"),
      });
    }
  };

  const dirty = Object.values(form).some((v) => v.trim() !== "");
  return (
    <ConfirmModal
      isOpen={!!id}
      onClose={handleClose}
      onConfirm={handleLease}
      type="warning"
      title="Lease budget to this capability?"
      message={
        <p>
          Charges of capability{" "}
          <span className="font-mono text-foreground">{id}</span>, and of those
          delegated from it, are taken from the lease instead of locking every
          ancestor and the tenant.
        </p>
      }
      warning="What the lease holds counts against every ancestor and the tenant until it is settled, so other capabilities cannot spend it."
      confirmText="Lease"
      confirmDisabled={!parsed || parsed.error !== undefined}
    >
      <div className="space-y-3 text-left">
        {FIELDS.map((f) => (
          <div key={f.key} className="space-y-1.5">
            <Label className="text-xs" htmlFor={`lease-${f.key}`}>
              {f.label(unit)}
            </Label>
            <Input
              id={`lease-${f.key}`}
              inputMode="decimal"
              placeholder={f.placeholder}
              value={form[f.key]}
              onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
            />
          </div>
        ))}
        {dirty && parsed?.error ? (
          <p role="alert" className="text-xs text-destructive">
            {parsed.error}
          </p>
        ) : null}
      </div>
    </ConfirmModal>
  );
}

/**
 * Confirms settling a capability's lease: what it spent is charged to every
 * ancestor and the tenant, and the rest is released for them to spend.
 */
export function SettleLeaseDialog({
  id,
  onClose,
}: {
  /** The capability whose lease to settle; null keeps the dialog closed. */
  id: string | null;
  onClose: () => void;
}) {
  const { showNotification } = useNotification();
  const queryClient = useQueryClient();

  const handleSettle = async () => {
    if (!id) return;
    try {
      await capabilityClient.settleLease({ id });
      showNotification({
        type: "success",
        title: "Lease settled",
        message: `Capability ${id}'s lease is settled; what it did not spend is released.`,
      });
      void queryClient.invalidateQueries({
        queryKey: capabilityLeaseQuery(id).queryKey,
      });
      onClose();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Settle failed",
        message: errorMessage(err, "The lease was not settled"),
      });
    }
  };

  return (
    <ConfirmModal
      isOpen={!!id}
      onClose={onClose}
      onConfirm={handleSettle}
      type="warning"
      title="Settle this lease?"
      message={
        <p>
          What capability{" "}
          <span className="font-mono text-foreground">{id}</span> spent under
          its lease is charged to every ancestor and the tenant, and the rest is
          released.
        </p>
      }
      warning="Its charges go through the whole chain again until it leases anew."
      confirmText="Settle lease"
    />
  );
}
