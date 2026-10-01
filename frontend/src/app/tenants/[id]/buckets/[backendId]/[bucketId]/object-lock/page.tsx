"use client";

// Bucket object-lock tab — configures write-once-read-many retention via
// BucketService.SetObjectLock. When enabled, objects written to the bucket
// carry a default retention: GOVERNANCE mode is bypassable by a privileged
// role, COMPLIANCE mode is not bypassable until the retention expires. The
// backend persists ObjectLockConfig on the bucket row and the data plane
// enforces it on delete/overwrite; this page is the operator switch.

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNotification } from "@/components/ui/Notification";
import { Switch } from "@/components/ui/switch";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";

import { bucketClient } from "@/lib/connect/client";
import { ObjectLockMode } from "@/gen/paladin/admin/v1/types_pb";

import { useBucket } from "../bucket-context";
import { errorMessage } from "@/hooks/errorContract";

const DAY_SECONDS = 86_400;

// The Select works in strings; map to/from the enum at the edges.
function modeToStr(m: ObjectLockMode): "governance" | "compliance" {
  return m === ObjectLockMode.COMPLIANCE ? "compliance" : "governance";
}
function strToMode(s: string): ObjectLockMode {
  return s === "compliance"
    ? ObjectLockMode.COMPLIANCE
    : ObjectLockMode.GOVERNANCE;
}

export default function BucketObjectLockPage() {
  const { bucket, setBucket } = useBucket();
  const { showNotification } = useNotification();

  const current = bucket.objectLock;
  const currentEnabled = current?.enabled ?? false;
  const currentMode = modeToStr(
    current?.defaultMode ?? ObjectLockMode.GOVERNANCE,
  );
  const currentDays = current?.defaultRetention?.seconds
    ? String(Number(current.defaultRetention.seconds) / DAY_SECONDS)
    : "";

  const [enabled, setEnabled] = useState(currentEnabled);
  const [mode, setMode] = useState<"governance" | "compliance">(currentMode);
  const [days, setDays] = useState(currentDays);
  const [saving, setSaving] = useState(false);

  const dirty =
    enabled !== currentEnabled ||
    (enabled && mode !== currentMode) ||
    (enabled && days.trim() !== currentDays);

  const daysNum = Number(days);
  const daysValid =
    days.trim() === "" || (Number.isFinite(daysNum) && daysNum >= 0);

  const save = async () => {
    setSaving(true);
    try {
      const retentionSeconds =
        enabled && daysNum > 0 ? BigInt(Math.round(daysNum * DAY_SECONDS)) : 0n;
      const updated = await bucketClient.setObjectLock({
        name: bucket.name,
        resourceVersion: bucket.resourceVersion,
        config: {
          enabled,
          // Mode/retention only carry meaning while locking is on; send neutral
          // values when disabling so the server clears them.
          defaultMode: enabled ? strToMode(mode) : ObjectLockMode.UNSPECIFIED,
          defaultRetention:
            retentionSeconds > 0n
              ? { seconds: retentionSeconds, nanos: 0 }
              : undefined,
        },
      });
      setBucket(updated);
      showNotification({ type: "success", title: "Object lock updated" });
    } catch (e) {
      showNotification({
        type: "error",
        title: "Save failed",
        message: errorMessage(e, "Failed to update object lock"),
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="max-w-2xl space-y-6 p-6">
      <div>
        <h2 className="text-lg font-semibold text-white">Object lock</h2>
        <p className="mt-1 text-sm text-slate-400">
          Write-once-read-many retention. Objects can&apos;t be deleted or
          overwritten until their retention expires.
        </p>
      </div>

      <div className="flex items-center justify-between gap-4">
        <div>
          <Label htmlFor="lock-enabled" className="text-sm text-white">
            Enable object lock
          </Label>
          <p className="text-xs text-slate-500">
            Applies a default retention to new objects.
          </p>
        </div>
        <Switch
          id="lock-enabled"
          checked={enabled}
          onCheckedChange={setEnabled}
        />
      </div>

      <div className="flex items-center justify-between gap-4">
        <div>
          <Label className="text-sm text-white">Default mode</Label>
          <p className="text-xs text-slate-500">
            Governance is bypassable by a privileged role; Compliance is not,
            until expiry.
          </p>
        </div>
        <SelectRoot
          value={mode}
          onValueChange={(v) => setMode(v as "governance" | "compliance")}
          disabled={!enabled}
        >
          <SelectTrigger className="w-44" aria-label="Default mode">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="governance">Governance</SelectItem>
            <SelectItem value="compliance">Compliance</SelectItem>
          </SelectContent>
        </SelectRoot>
      </div>

      <div className="flex items-center justify-between gap-4">
        <div>
          <Label htmlFor="lock-days" className="text-sm text-white">
            Default retention (days)
          </Label>
          <p className="text-xs text-slate-500">
            Blank or 0 sets no default retention window.
          </p>
        </div>
        <Input
          id="lock-days"
          type="number"
          min="0"
          placeholder="30"
          className="w-28"
          value={days}
          disabled={!enabled}
          onChange={(e) => setDays(e.target.value)}
        />
      </div>

      <div className="flex justify-end">
        <Button onClick={save} disabled={!dirty || !daysValid || saving}>
          {saving ? "Saving…" : "Save"}
        </Button>
      </div>
    </Card>
  );
}
