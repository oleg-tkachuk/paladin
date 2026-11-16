"use client";

import React from "react";
import { ShieldExclamationIcon } from "@heroicons/react/24/outline";

import type { TaintSignal } from "@/gen/paladin/data/v1/types_pb";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { IconTone } from "@/components/ui/IconChip";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useTenantChangesBlocked } from "@/app/tenants/[id]/tenant-context";
import { TAINT_SIGNALS, clearedBy, taintLabel } from "@/lib/objects/taint";
import { T } from "@/lib/ui/typography";

/**
 * The object's taint signals, and the way to change them (SetObjectTaint). A
 * capability without allow_tainted_read is refused any read of a tainted
 * object, so this card is where an operator learns why an agent was refused
 * and decides whether it should be.
 *
 * Adding a signal only narrows who reads the object and saves at once.
 * Clearing one re-opens the object to agents not allowed tainted content, so
 * a save that clears any asks first and names what it clears. The server
 * gates the call by its own Cedar action; a refusal stays in the dialog.
 */
export function ObjectTaintCard({
  signals,
  onSave,
}: {
  signals: readonly TaintSignal[];
  onSave: (signals: TaintSignal[]) => Promise<void>;
}) {
  const changesBlocked = useTenantChangesBlocked();
  const [draft, setDraft] = React.useState<TaintSignal[]>([...signals]);
  const [saving, setSaving] = React.useState(false);
  const [confirming, setConfirming] = React.useState(false);

  // The object reloads after a save, or under the card; the draft follows it.
  // Reset while rendering, from the signals it was last seeded with, rather
  // than in an effect that would paint the stale draft first.
  const current = signals.join(",");
  const [seededFrom, setSeededFrom] = React.useState(current);
  if (seededFrom !== current) {
    setSeededFrom(current);
    setDraft([...signals]);
  }

  const ordered = TAINT_SIGNALS.map((s) => s.signal).filter((s) =>
    draft.includes(s),
  );
  const changed = ordered.join(",") !== current;
  const cleared = clearedBy(signals, ordered);

  const toggle = (signal: TaintSignal, on: boolean) =>
    setDraft((d) => (on ? [...d, signal] : d.filter((s) => s !== signal)));

  async function save() {
    setSaving(true);
    try {
      await onSave(ordered);
    } catch {
      // Surfaced by the save hook.
    } finally {
      setSaving(false);
    }
  }

  // A refusal is thrown for the confirmation to show.
  async function confirmClear() {
    await onSave(ordered);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle
          className="text-sm font-semibold"
          icon={ShieldExclamationIcon}
          tone={signals.length > 0 ? IconTone.Warning : IconTone.Neutral}
        >
          Taint
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className={T.hint}>
          {signals.length > 0
            ? `Agents not allowed tainted content are refused this object: ${taintLabel(signals)}.`
            : "Not tainted: every agent that may read the collection may read this object."}
        </p>
        <fieldset className="space-y-2" disabled={Boolean(changesBlocked)}>
          <legend className="sr-only">Taint signals</legend>
          {TAINT_SIGNALS.map((s) => {
            const id = `taint-${s.signal}`;
            return (
              <div key={s.signal} className="flex items-start gap-2.5">
                <Checkbox
                  id={id}
                  checked={draft.includes(s.signal)}
                  onCheckedChange={(v) => toggle(s.signal, v === true)}
                  disabled={Boolean(changesBlocked)}
                  className="mt-0.5"
                />
                <label htmlFor={id} className="space-y-0.5 text-sm">
                  <span className="block font-medium text-foreground">
                    {s.label}
                  </span>
                  <span className={T.hint}>{s.description}</span>
                </label>
              </div>
            );
          })}
        </fieldset>
        <div className="flex justify-end">
          <Button
            size="sm"
            disabled={!changed || saving || Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
            onClick={() => (cleared.length > 0 ? setConfirming(true) : save())}
          >
            Save taint
          </Button>
        </div>
      </CardContent>

      <ConfirmModal
        isOpen={confirming}
        onClose={() => setConfirming(false)}
        onConfirm={confirmClear}
        type="warning"
        title="Clear taint"
        message={`Clearing ${taintLabel(cleared)} re-opens this object to agents not allowed tainted content.`}
        warning="Clear a signal only once the content no longer carries it."
        confirmText="Clear"
      />
    </Card>
  );
}
