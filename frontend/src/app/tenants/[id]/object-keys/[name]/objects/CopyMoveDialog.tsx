"use client";

// Copy / Move dialog for a single object, extracted from the objects page.
// Fully controlled: the page owns the copyMove state + the copy/move RPC
// (handleCopyMove, which closes over the loaded objects), this component just
// renders the destination-key input and delegates confirm/close.
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
import { T } from "@/lib/ui/typography";

export function CopyMoveDialog({
  open,
  type,
  sourceKey,
  destKey,
  onDestKeyChange,
  onConfirm,
  onClose,
}: {
  open: boolean;
  type: "copy" | "move";
  sourceKey: string;
  destKey: string;
  onDestKeyChange: (value: string) => void;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{type === "copy" ? "Copy" : "Move"} object</DialogTitle>
          <DialogDescription>
            {type === "copy"
              ? "Server-side copy. The source object remains in place."
              : "Soft-deletes the source after a successful copy. The original can be restored from Trash."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="space-y-1.5">
            <Label>Source path</Label>
            <Input
              disabled
              value={sourceKey}
              className="font-mono text-xs text-muted-foreground"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="dest-key">Destination key</Label>
            <Input
              id="dest-key"
              autoFocus
              value={destKey}
              onChange={(e) => onDestKeyChange(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && onConfirm()}
              className="font-mono text-xs"
            />
            <p className={T.hint}>
              Full path including filename, within the same ObjectKey.
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={onConfirm}>
            Confirm {type === "copy" ? "copy" : "move"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
