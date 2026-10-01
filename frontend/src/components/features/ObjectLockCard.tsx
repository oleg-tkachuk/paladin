"use client";

import React from "react";
import { LockClosedIcon, ScaleIcon } from "@heroicons/react/24/outline";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { T } from "@/lib/ui/typography";
import { useObjectLock } from "@/hooks/useObjectLock";
import { formatDateTime } from "@/lib/format/locale";

/**
 * Object Lock card (ADR-0013).
 *
 * The card's job is to make the difference between the two modes legible
 * before the user commits, because the API cannot undo the choice afterwards:
 *
 *   GOVERNANCE — an operator holding `lock.governance.bypass` can shorten the
 *   window or delete through it. Protects against accident.
 *
 *   COMPLIANCE — nobody can shorten it. Not the tenant admin, not the
 *   platform admin, not the person setting it now. The object, and the
 *   storage it occupies, are pinned until the date passes.
 *
 * So COMPLIANCE gets an explicit confirmation step rather than a save button,
 * and the copy states the consequence in the same breath as asking.
 *
 * Legal hold is presented separately because it behaves differently: no
 * expiry, and reversible by whoever may set it.
 */
export function ObjectLockCard({ objectName }: { objectName: string }) {
  const { lock, isLoading, error, setRetention, setLegalHold } =
    useObjectLock(objectName);

  const [mode, setMode] = React.useState<"GOVERNANCE" | "COMPLIANCE">(
    "GOVERNANCE",
  );
  const [until, setUntil] = React.useState("");
  const [bypass, setBypass] = React.useState(false);
  const [confirming, setConfirming] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  const activeUntil = lock?.retainUntil
    ? new Date(Number(lock.retainUntil.seconds) * 1000)
    : null;
  const retained = activeUntil !== null && activeUntil > new Date();
  const held = lock?.legalHold ?? false;

  const apply = async () => {
    if (!until) return;
    setSaving(true);
    try {
      await setRetention({
        mode,
        retainUntil: new Date(until),
        bypassGovernance: bypass,
      });
      setConfirming(false);
      setUntil("");
      setBypass(false);
    } catch {
      // Surfaced by the hook's notification.
    } finally {
      setSaving(false);
    }
  };

  const toggleHold = async (next: boolean) => {
    setSaving(true);
    try {
      await setLegalHold(next);
    } catch {
      // Surfaced by the hook's notification.
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h3 className={`${T.cardTitleProse} flex items-center gap-2`}>
          <LockClosedIcon className="size-4" aria-hidden="true" />
          Object Lock
        </h3>
        {retained && (
          <Badge
            variant={lock?.mode === "COMPLIANCE" ? "destructive" : "secondary"}
          >
            {lock?.mode}
          </Badge>
        )}
      </div>

      {isLoading && <p className={T.hint}>Reading lock state…</p>}
      {error && <p className={T.hint}>{error}</p>}

      {!isLoading && !error && (
        <>
          <div className="space-y-1">
            {retained ? (
              <p className={T.hint}>
                Retained under <strong>{lock?.mode}</strong> until{" "}
                <time dateTime={activeUntil?.toISOString()}>
                  {activeUntil ? formatDateTime(activeUntil) : null}
                </time>
                .{" "}
                {lock?.mode === "COMPLIANCE"
                  ? "This window cannot be shortened by anyone."
                  : "Shortening this window requires the governance bypass."}
              </p>
            ) : (
              <p className={T.hint}>
                No retention window. This version can be deleted.
              </p>
            )}
          </div>

          {/* ─── Retention ─────────────────────────────────────────────── */}
          <div className="space-y-2 border-t pt-3">
            <label className={T.label} htmlFor="lock-mode">
              {retained ? "Extend or tighten retention" : "Apply retention"}
            </label>
            <div className="flex flex-wrap items-center gap-2">
              <SelectRoot
                value={mode}
                onValueChange={(v) => {
                  setMode(v as "GOVERNANCE" | "COMPLIANCE");
                  setConfirming(false);
                }}
              >
                <SelectTrigger id="lock-mode" className="w-[180px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="GOVERNANCE">GOVERNANCE</SelectItem>
                  <SelectItem value="COMPLIANCE">COMPLIANCE</SelectItem>
                </SelectContent>
              </SelectRoot>

              <Input
                type="datetime-local"
                aria-label="Retain until"
                value={until}
                onChange={(e) => {
                  setUntil(e.target.value);
                  setConfirming(false);
                }}
                className="w-[230px]"
              />
            </div>

            {mode === "COMPLIANCE" && (
              <p className={T.hint}>
                COMPLIANCE cannot be shortened, downgraded or removed — not by a
                tenant admin, not by a platform admin, not by you. The object
                and its storage are pinned until this date passes.
              </p>
            )}

            {retained && lock?.mode === "GOVERNANCE" && (
              <label className="flex items-center gap-2">
                <Switch checked={bypass} onCheckedChange={setBypass} />
                <span className={T.hint}>
                  Use governance bypass (needed only to shorten the current
                  window; requires the <code>lock.governance.bypass</code> role)
                </span>
              </label>
            )}

            {mode === "COMPLIANCE" && !confirming ? (
              <Button
                variant="outline"
                disabled={!until || saving}
                onClick={() => setConfirming(true)}
              >
                Apply COMPLIANCE retention…
              </Button>
            ) : (
              <div className="flex items-center gap-2">
                <Button disabled={!until || saving} onClick={apply}>
                  {mode === "COMPLIANCE"
                    ? "Yes — pin permanently until that date"
                    : "Apply retention"}
                </Button>
                {confirming && (
                  <Button
                    variant="ghost"
                    disabled={saving}
                    onClick={() => setConfirming(false)}
                  >
                    Cancel
                  </Button>
                )}
              </div>
            )}
          </div>

          {/* ─── Legal hold ────────────────────────────────────────────── */}
          <div className="space-y-2 border-t pt-3">
            <div className="flex items-center justify-between gap-3">
              <label
                className={`${T.label} flex items-center gap-2`}
                htmlFor="legal-hold"
              >
                <ScaleIcon className="size-4" aria-hidden="true" />
                Legal hold
              </label>
              <Switch
                id="legal-hold"
                checked={held}
                disabled={saving}
                onCheckedChange={toggleHold}
              />
            </div>
            <p className={T.hint}>
              {held
                ? "On. Deletion is blocked for as long as the hold stands, regardless of any retention window, and the governance bypass does not lift it."
                : "Off. A hold blocks deletion indefinitely and can be lifted again — use it when the release date is not yet known."}
            </p>
          </div>
        </>
      )}
    </Card>
  );
}
