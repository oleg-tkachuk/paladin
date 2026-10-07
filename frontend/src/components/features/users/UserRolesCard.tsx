"use client";

/**
 * A user's roles, edited in place. Roles the console does not offer — above
 * all platform.admin — are shown and kept as they are: the console can take a
 * role it offers away, but not one it would never grant.
 */

import { useState } from "react";

import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { useNotification } from "@/components/ui/Notification";
import { useUserAdmin } from "@/hooks/useUserAdmin";
import {
  ADMIN_AUDIENCE_ROLES,
  ASSIGNABLE_ROLES,
  ROLE_DESCRIPTIONS,
} from "@/constants/roles";
import { T } from "@/lib/ui/typography";

interface UserForRoles {
  name: string;
  roles: string[];
  resourceVersion: string;
}

function sameSet(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((r) => b.includes(r));
}

export function UserRolesCard({
  user,
  onChanged,
}: {
  user: UserForRoles;
  onChanged: () => void;
}) {
  const { busy, updateUser } = useUserAdmin();
  const { showNotification } = useNotification();
  const [draft, setDraft] = useState<string[]>(user.roles);
  // A new read (after a save, or another operator's edit) resets the draft.
  const [readVersion, setReadVersion] = useState(user.resourceVersion);
  if (readVersion !== user.resourceVersion) {
    setReadVersion(user.resourceVersion);
    setDraft(user.roles);
  }

  const kept = user.roles.filter((r) => !ASSIGNABLE_ROLES.includes(r));
  const changed = !sameSet(draft, user.roles);

  function toggle(role: string) {
    setDraft((d) =>
      d.includes(role) ? d.filter((r) => r !== role) : [...d, role],
    );
  }

  async function save() {
    const res = await updateUser({
      name: user.name,
      resourceVersion: user.resourceVersion,
      roles: draft,
    });
    if (!res.ok) {
      showNotification({
        type: "error",
        title: "Could not save roles",
        message: res.error ?? "",
      });
      return;
    }
    showNotification({ type: "success", title: "Roles saved", message: "" });
    onChanged();
  }

  return (
    <Card className="space-y-4 p-5">
      <div className="space-y-1">
        <h2 className="text-sm font-semibold">Roles</h2>
        <p className={T.hint}>
          What the user may do, as Cedar policy reads it. Tokens already issued
          keep their roles until they expire.
        </p>
      </div>

      {kept.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className={T.hint}>Kept as granted:</span>
          {kept.map((r) => (
            <Badge key={r} variant="outline" className="font-mono">
              {r}
            </Badge>
          ))}
        </div>
      )}

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {ASSIGNABLE_ROLES.map((role) => {
          const id = `user-role-${role}`;
          return (
            <label
              key={role}
              htmlFor={id}
              className="flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 has-[[data-state=checked]]:border-primary/50"
            >
              <Checkbox
                id={id}
                className="mt-0.5"
                checked={draft.includes(role)}
                onCheckedChange={() => toggle(role)}
              />
              <span className="min-w-0 space-y-0.5">
                <span className="block font-mono text-xs">
                  {role}
                  {ADMIN_AUDIENCE_ROLES.includes(role) ? (
                    <span className="ml-1.5 font-sans text-tiny text-muted-foreground">
                      console
                    </span>
                  ) : null}
                </span>
                {ROLE_DESCRIPTIONS[role] ? (
                  <span className="block text-xs text-muted-foreground">
                    {ROLE_DESCRIPTIONS[role]}
                  </span>
                ) : null}
              </span>
            </label>
          );
        })}
      </div>

      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          disabled={!changed || busy}
          onClick={() => setDraft(user.roles)}
        >
          Discard
        </Button>
        <Button disabled={!changed || busy} onClick={() => void save()}>
          {busy ? "Saving…" : "Save roles"}
        </Button>
      </div>
    </Card>
  );
}
