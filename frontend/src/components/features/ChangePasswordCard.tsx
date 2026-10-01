"use client";

/**
 * Password change for the signed-in user.
 *
 * The backend has exposed AuthService.ChangePassword since IAM landed; the
 * console never called it, so a user's only route to a new password was an
 * operator running SQL. That is the kind of gap that turns a routine
 * credential rotation into a support ticket.
 *
 * The 12-character floor is the server's (`min_len = 12` on the proto), not
 * a second opinion invented here — checking it client-side turns a round
 * trip into an inline message, but the server remains the authority.
 */

import { useState } from "react";

import { authClient } from "@/lib/connect/client";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNotification } from "@/components/ui/Notification";
import { errorMessage } from "@/hooks/errorContract";

/** Mirrors `buf.validate` on ChangePasswordRequest.new_password. */
const MIN_PASSWORD_LENGTH = 12;

export function ChangePasswordCard() {
  const { showNotification } = useNotification();
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  const tooShort =
    newPassword.length > 0 && newPassword.length < MIN_PASSWORD_LENGTH;
  const mismatch = confirm.length > 0 && confirm !== newPassword;
  const canSubmit =
    !busy &&
    oldPassword.length > 0 &&
    newPassword.length >= MIN_PASSWORD_LENGTH &&
    confirm === newPassword;

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
          Change the password for this account. Requires the current one.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-6">
        <form onSubmit={submit} className="max-w-md space-y-4">
          <div className="space-y-2">
            <Label htmlFor="current-password">Current password</Label>
            <Input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={oldPassword}
              onChange={(e) => setOldPassword(e.target.value)}
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="new-password">New password</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              aria-invalid={tooShort || undefined}
              aria-describedby="new-password-hint"
            />
            <p
              id="new-password-hint"
              className={
                tooShort
                  ? "text-xs text-destructive"
                  : "text-xs text-muted-foreground"
              }
            >
              At least {MIN_PASSWORD_LENGTH} characters.
            </p>
          </div>

          <div className="space-y-2">
            <Label htmlFor="confirm-password">Confirm new password</Label>
            <Input
              id="confirm-password"
              type="password"
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
        </form>
      </CardContent>
    </Card>
  );
}
