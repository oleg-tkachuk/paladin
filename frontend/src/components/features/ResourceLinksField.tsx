"use client";

// The profile's switch for the sidebar's "Pinned & recent" section: the way
// back after "Hide this section", and the way to turn it off from here.
// Saved at once; the lists themselves are kept while it is off.
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useResourceLinks } from "@/hooks/useResourceLinks";
import { T } from "@/lib/ui/typography";

export function ResourceLinksField() {
  const { shown, setShown } = useResourceLinks();
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="space-y-1">
        <Label htmlFor="resource-links-shown">Pinned &amp; recent</Label>
        <p className={T.hint}>
          List the resources you pinned and opened lately at the top of the
          sidebar. Off, the lists are kept and come back with it.
        </p>
      </div>
      <Switch
        id="resource-links-shown"
        checked={shown}
        onCheckedChange={(on) => void setShown(on).catch(() => undefined)}
      />
    </div>
  );
}
