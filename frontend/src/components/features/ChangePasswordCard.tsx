"use client";

/**
 * Password change for the signed-in user (AuthService.ChangePassword).
 *
 * The rules are listed as the user types, each ticked when met, so a refusal
 * is never the first time the user hears of one. The length floor is the
 * contract's (`min_len` on ChangePasswordRequest.new_password, read from the
 * generated descriptor); the server remains the authority.
 */

import { useState } from "react";
import { CheckCircleIcon } from "@heroicons/react/24/solid";

import { authClient } from "@/lib/connect/client";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/PasswordInput";
import { useNotification } from "@/components/ui/Notification";
import { errorMessage } from "@/hooks/errorContract";
import { NEW_PASSWORD_MIN_LENGTH } from "@/lib/userPrefs";
import { cn } from "@/lib/utils";

export function ChangePasswordCard() {
  const { showNotification } = useNotification();
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const rules = [
    {
      label: `At least ${NEW_PASSWORD_MIN_LENGTH} characters`,
      met: newPassword.length >= NEW_PASSWORD_MIN_LENGTH,
    },
    {
      label: "Not the current password",
      met: newPassword.length > 0 && newPassword !== oldPassword,
    },
    {
      label: "Typed the same twice",
      met: newPassword.length > 0 && confirm === newPassword,
    },
  ];
  const mismatch = confirm.length > 0 && confirm !== newPassword;
  const canSubmit =
    !busy && oldPassword.length > 0 && rules.every((r) => r.met);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    try {
      await authClient.changePassword({ oldPassword, newPassword });
      setOldPassword("");
      setNewPassword("");
      setConfirm("");
      showNotification({
        type: "success",
        title: "Password changed",
        // Sessions are not invalidated server-side, so say so rather than
        // letting the user assume other devices were signed out.
        message: "Existing sessions stay signed in until their tokens expire.",
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Could not change password",
        message: errorMessage(err, "Unexpected error. Try again."),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader className="px-6">
        <CardTitle className="text-base">Password</CardTitle>
        <CardDescription>
          Requires the current one. Other sessions stay signed in until their
          tokens expire.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-6">
        <form
          onSubmit={submit}
          className="grid gap-x-8 gap-y-4 md:grid-cols-[minmax(0,22rem)_1fr]"
        >
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="current-password">Current password</Label>
              <PasswordInput
                id="current-password"
                autoComplete="current-password"
                value={oldPassword}
                onChange={(e) => setOldPassword(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="new-password">New password</Label>
              <PasswordInput
                id="new-password"
                autoComplete="new-password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                aria-describedby="new-password-rules"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm-password">Confirm new password</Label>
              <PasswordInput
                id="confirm-password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                aria-invalid={mismatch || undefined}
                aria-describedby={mismatch ? "confirm-error" : undefined}
              />
              {mismatch && (
                <p id="confirm-error" className="text-xs text-destructive">
                  Does not match the new password.
                </p>
              )}
            </div>
            <Button type="submit" disabled={!canSubmit}>
              {busy ? "Changing…" : "Change password"}
            </Button>
          </div>
          <ul
            id="new-password-rules"
            aria-label="Password requirements"
            className="space-y-1.5 self-start text-sm md:pt-7"
          >
            {rules.map((r) => (
              <li
                key={r.label}
                className={cn(
                  "flex items-center gap-2",
                  r.met ? "text-foreground" : "text-muted-foreground",
                )}
              >
                <CheckCircleIcon
                  className={cn(
                    "size-4 shrink-0",
                    r.met ? "text-success" : "text-muted-foreground/40",
                  )}
                  aria-hidden
                />
                {r.label}
                <span className="sr-only">{r.met ? "(met)" : "(not yet)"}</span>
              </li>
            ))}
          </ul>
        </form>
      </CardContent>
    </Card>
  );
}
