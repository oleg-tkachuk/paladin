"use client";

// The profile's switch for browser notifications about the user's own
// operations. Saved at once, like the theme menu would be, since turning it
// on asks the browser for its permission there and then.
import { useState } from "react";

import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { usePreference } from "@/hooks/usePreference";
import { PREFERENCE_KEYS, readFlag } from "@/lib/preferences/keys";
import { T } from "@/lib/ui/typography";

/** What the browser says when a site may not notify. */
const DENIED = "denied";
const GRANTED = "granted";

/** Why the switch cannot do what it says, or null when it can. */
export function notifyBlocker(
  supported: boolean,
  permission: string | undefined,
): string | null {
  if (!supported) return "This browser cannot show notifications.";
  if (permission === DENIED) {
    return "Notifications are blocked for this site in the browser's settings.";
  }
  return null;
}

export function NotifyOperationsField() {
  const [enabled, setEnabled] = usePreference(
    PREFERENCE_KEYS.notifyOperations,
    readFlag,
  );
  const supported = typeof Notification !== "undefined";
  const [permission, setPermission] = useState(
    supported ? Notification.permission : undefined,
  );
  const blocker = notifyBlocker(supported, permission);

  const onChange = async (on: boolean) => {
    if (on && supported && Notification.permission !== GRANTED) {
      const answer = await Notification.requestPermission();
      setPermission(answer);
      if (answer !== GRANTED) return;
    }
    await setEnabled(on);
  };

  return (
    <div className="flex items-start justify-between gap-4">
      <div className="space-y-1">
        <Label htmlFor="notify-operations">Operation notifications</Label>
        <p className={T.hint}>
          A browser notification when one of your operations finishes while the
          console is in a background tab.
        </p>
        {blocker ? (
          <p role="status" className="text-xs text-warning">
            {blocker}
          </p>
        ) : null}
      </div>
      <Switch
        id="notify-operations"
        checked={enabled && !blocker}
        disabled={!supported}
        onCheckedChange={(on) => void onChange(on)}
      />
    </div>
  );
}
