import React from "react";
import { CheckIcon, PlusIcon, TrashIcon } from "@heroicons/react/24/outline";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";

/**
 * The object's free-form Tags card, extracted from ObjectDetailView. It owns
 * the edit draft (the working label map + the new-key/new-value inputs +
 * saving state); the parent owns the isEditing flag (so both the header
 * "Edit Metadata" button and this card's "Edit" button can toggle it) and
 * the save RPC, passed as onSave.
 *
 * The reserved `object_tag` key is filtered out of both view and edit modes —
 * it's the classification slug rendered in the Specs panel, not a free-form
 * tag, and hiding it here prevents the user from clearing it by accident.
 */
export function ObjectTagsCard({
  tags,
  isEditing,
  onEditToggle,
  onSave,
}: {
  tags: Record<string, string> | undefined;
  isEditing: boolean;
  onEditToggle: (editing: boolean) => void;
  onSave: (labels: Record<string, string>) => Promise<void>;
}) {
  const [editingLabels, setEditingLabels] = React.useState<
    Record<string, string>
  >({});
  const [newLabelKey, setNewLabelKey] = React.useState("");
  const [newLabelValue, setNewLabelValue] = React.useState("");
  const [isSaving, setIsSaving] = React.useState(false);

  // Seed the draft from the current tags whenever edit mode opens — and ONLY
  // then. `tags` is read through a ref (kept current in its own effect) so a
  // background refetch mid-edit can't re-run the seed effect and silently wipe
  // the user's unsaved changes — the exact regression the original guarded
  // against by not syncing the draft reactively from the object.
  const tagsRef = React.useRef(tags);
  React.useEffect(() => {
    tagsRef.current = tags;
  }, [tags]);
  React.useEffect(() => {
    if (isEditing) {
      setEditingLabels(tagsRef.current ? { ...tagsRef.current } : {});
    }
  }, [isEditing]);

  const removeLabel = (key: string) => {
    const next = { ...editingLabels };
    delete next[key];
    setEditingLabels(next);
  };

  const addLabel = () => {
    if (!newLabelKey || !newLabelValue) return;
    setEditingLabels({ ...editingLabels, [newLabelKey]: newLabelValue });
    setNewLabelKey("");
    setNewLabelValue("");
  };

  const handleSave = async () => {
    try {
      setIsSaving(true);
      await onSave(editingLabels);
      onEditToggle(false);
    } catch {
      // Error surfaced by the save hook.
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <Card className="space-y-3 p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Tags</h2>
        <Button
          variant="outline"
          size="sm"
          onClick={() => onEditToggle(!isEditing)}
        >
          {isEditing ? "Cancel" : "Edit"}
        </Button>
      </div>

      {isEditing ? (
        <div className="space-y-3">
          <div className="grid grid-cols-[1fr_1fr_auto] gap-2">
            <Input
              placeholder="TAG_KEY"
              aria-label="New tag key"
              value={newLabelKey}
              onChange={(e) => setNewLabelKey(e.target.value.toUpperCase())}
              className="font-mono text-xs"
            />
            <Input
              placeholder="TAG_VALUE"
              aria-label="New tag value"
              value={newLabelValue}
              onChange={(e) => setNewLabelValue(e.target.value)}
              className="font-mono text-xs"
            />
            <Button
              size="sm"
              onClick={addLabel}
              disabled={!newLabelKey || !newLabelValue}
            >
              <PlusIcon className="size-4" />
              Add
            </Button>
          </div>

          <div className="space-y-1.5">
            {Object.entries(editingLabels).filter(([k]) => k !== "object_tag")
              .length === 0 ? (
              <p className={T.hint}>No tags assigned.</p>
            ) : (
              Object.entries(editingLabels)
                .filter(([k]) => k !== "object_tag")
                .map(([key, value]) => (
                  <div
                    key={key}
                    className="flex items-center gap-2 rounded-md border border-border bg-muted/40 p-2"
                  >
                    <span className="font-mono text-xs text-muted-foreground">
                      {key}
                    </span>
                    <span className="text-muted-foreground">=</span>
                    <span className="flex-1 break-all font-mono text-xs">
                      {value}
                    </span>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => removeLabel(key)}
                      aria-label="Remove tag"
                    >
                      <TrashIcon className="size-3.5" />
                    </Button>
                  </div>
                ))
            )}
          </div>

          <div className="flex justify-end">
            <Button size="sm" onClick={handleSave} disabled={isSaving}>
              <CheckIcon className="size-4" />
              {isSaving ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
      ) : (
        (() => {
          // View mode also filters object_tag — its value is the
          // classification slug rendered in the Specs panel.
          const freeFormTags = Object.entries(tags || {}).filter(
            ([k]) => k !== "object_tag",
          );
          return freeFormTags.length > 0 ? (
            <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
              {freeFormTags.map(([key, value]) => (
                <Badge
                  key={key}
                  variant="outline"
                  className="justify-start font-mono text-xs"
                >
                  {key}={value}
                </Badge>
              ))}
            </div>
          ) : (
            <p className={T.hint}>No tags assigned.</p>
          );
        })()
      )}
    </Card>
  );
}
