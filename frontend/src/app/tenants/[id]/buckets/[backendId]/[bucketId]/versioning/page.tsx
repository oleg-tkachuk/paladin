"use client";

// Bucket versioning tab — toggles the BucketVersioning state via
// BucketService.SetVersioning. The backend persists it on the bucket row and
// the object data-plane honours it (soft-deletes preserve prior versions); this
// page is the operator switch + retention option.

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Label } from "@/components/ui/label";
import { useNotification } from "@/components/ui/Notification";
import { Switch } from "@/components/ui/switch";

import { bucketClient } from "@/lib/connect/client";

import { useBucket } from "../bucket-context";
import { errorMessage } from "@/hooks/errorContract";

export default function BucketVersioningPage() {
  const { bucket, setBucket } = useBucket();
  const { showNotification } = useNotification();

  const current = bucket.versioning;
  const [enabled, setEnabled] = useState(current?.enabled ?? false);
  const [keepDeletes, setKeepDeletes] = useState(
    current?.keepDeletesForever ?? false,
  );
  const [saving, setSaving] = useState(false);

  const dirty =
    enabled !== (current?.enabled ?? false) ||
    keepDeletes !== (current?.keepDeletesForever ?? false);

  const save = async () => {
    setSaving(true);
    try {
      const updated = await bucketClient.setVersioning({
        name: bucket.name,
        resourceVersion: bucket.resourceVersion,
        versioning: { enabled, keepDeletesForever: enabled && keepDeletes },
      });
      setBucket(updated);
      showNotification({ type: "success", title: "Versioning updated" });
    } catch (e) {
      showNotification({
        type: "error",
        title: "Save failed",
        message: errorMessage(e, "Failed to update versioning"),
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="max-w-2xl space-y-6 p-6">
      <div>
        <h2 className="text-lg font-semibold text-foreground">Versioning</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          When enabled, overwrites and deletes preserve prior object versions
          instead of replacing them in place.
        </p>
      </div>

      <div className="flex items-center justify-between gap-4">
        <div>
          <Label
            htmlFor="versioning-enabled"
            className="text-sm text-foreground"
          >
            Enable versioning
          </Label>
          <p className="text-xs text-muted-foreground">
            Keeps a version history per object.
          </p>
        </div>
        <Switch
          id="versioning-enabled"
          checked={enabled}
          onCheckedChange={setEnabled}
        />
      </div>

      <div className="flex items-center justify-between gap-4">
        <div>
          <Label htmlFor="keep-deletes" className="text-sm text-foreground">
            Keep deletes forever
          </Label>
          <p className="text-xs text-muted-foreground">
            Soft-deletes never reclaim storage; delete markers are retained.
          </p>
        </div>
        <Switch
          id="keep-deletes"
          checked={enabled && keepDeletes}
          disabled={!enabled}
          onCheckedChange={setKeepDeletes}
        />
      </div>

      <div className="flex justify-end">
        <Button onClick={save} disabled={!dirty || saving}>
          {saving ? "Saving…" : "Save"}
        </Button>
      </div>
    </Card>
  );
}
