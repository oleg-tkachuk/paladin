"use client";

// Copies the selected objects into a collection of this tenant, each under
// its own key behind an optional prefix. The page owns the selection and the
// RPC; this asks only where.
import { useEffect, useState } from "react";

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
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { useCollections } from "@/hooks/useCollections";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { copyTargetProblem } from "@/lib/batchSelector";
import { T } from "@/lib/ui/typography";

export function BulkCopyDialog({
  open,
  count,
  sourceCollection,
  onConfirm,
  onClose,
}: {
  open: boolean;
  count: number;
  sourceCollection: string;
  onConfirm: (destination: string, prefix: string) => Promise<void>;
  onClose: () => void;
}) {
  const { collections, error, fetchAllCollections } = useCollections();
  const [destination, setDestination] = useState(sourceCollection);
  const [prefix, setPrefix] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) void fetchAllCollections();
  }, [open, fetchAllCollections]);

  const problem = copyTargetProblem(sourceCollection, destination, prefix);
  const confirm = async () => {
    if (problem) return;
    setBusy(true);
    try {
      await onConfirm(destination, prefix);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Copy {count} objects</DialogTitle>
          <DialogDescription>
            Server-side copy, run as an operation. The objects stay where they
            are.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <Label htmlFor="bulk-copy-destination">
              Destination collection
            </Label>
            <SelectRoot value={destination} onValueChange={setDestination}>
              <SelectTrigger id="bulk-copy-destination">
                <SelectValue placeholder="Choose a collection" />
              </SelectTrigger>
              <SelectContent>
                {collections.map((c) => (
                  <SelectItem key={c.collection} value={c.collection}>
                    {c.displayName || c.collection}
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
            {error ? (
              <ListLoadError
                variant="inline"
                what="Collections"
                reason={error}
                onRetry={() => void fetchAllCollections()}
              />
            ) : null}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="bulk-copy-prefix">Key prefix</Label>
            <Input
              id="bulk-copy-prefix"
              value={prefix}
              onChange={(e) => setPrefix(e.target.value)}
              placeholder="e.g. backup/2026-10/"
              className="font-mono text-xs"
            />
            <p className={T.hint}>
              Each copy keeps its key, with this in front of it.
            </p>
          </div>
          {problem ? (
            <p role="alert" className="text-xs text-destructive">
              {problem}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void confirm()} disabled={!!problem || busy}>
            Copy {count} objects
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
