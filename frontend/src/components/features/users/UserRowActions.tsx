"use client";

/**
 * Per-user actions: disable/enable, reset password, delete.
 *
 * Every one carries the row's resourceVersion so a concurrent edit is
 * rejected by the server rather than silently overwritten — optimistic
 * concurrency is the point of that field, and dropping it would make the
 * console the one client that ignores it.
 */

import { useState } from "react";

import { Dropdown } from "@/components/ui/Dropdown";
import { Modal } from "@/components/ui/Modal";
import { Button } from "@/components/ui/button";
import { useNotification } from "@/components/ui/Notification";
import { useUserAdmin } from "@/hooks/useUserAdmin";

export interface ResetTarget {
  subject: string;
  password: string;
}

interface UserLike {
  name: string;
  subject: string;
  displayName: string;
  disabled: boolean;
  resourceVersion: string;
}

export function UserRowActions({
  user,
  onChanged,
  onResetPassword,
}: {
  user: UserLike;
  onChanged: () => void;
  onResetPassword: (t: ResetTarget) => void;
}) {
  const { busy, updateUser, deleteUser, resetPassword } = useUserAdmin();
  const { showNotification } = useNotification();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const label = user.displayName || user.subject;

  async function toggleDisabled() {
    const res = await updateUser({
      name: user.name,
      resourceVersion: user.resourceVersion,
      disabled: !user.disabled,
    });
    if (!res.ok) {
      showNotification({
        type: "error",
        title: user.disabled ? "Could not enable" : "Could not disable",
        message: res.error ?? "",
      });
      return;
    }
    showNotification({
      type: "success",
      title: user.disabled ? "User enabled" : "User disabled",
      message: label,
    });
    onChanged();
  }

  async function doReset() {
    const res = await resetPassword(user.name);
    if (!res.ok) {
      showNotification({
        type: "error",
        title: "Could not reset password",
        message: res.error ?? "",
      });
      return;
    }
    // Surfaced through the page, not a toast: this value cannot be
    // retrieved again, and a toast that scrolls away loses it.
    onResetPassword({ subject: user.subject, password: res.password ?? "" });
  }

  async function doDelete() {
    const res = await deleteUser({
      name: user.name,
      resourceVersion: user.resourceVersion,
    });
    setConfirmDelete(false);
    if (!res.ok) {
      showNotification({
        type: "error",
        title: "Could not delete user",
        message: res.error ?? "",
      });
      return;
    }
    showNotification({
      type: "success",
      title: "User deleted",
      message: label,
    });
    onChanged();
  }

  return (
    <>
      <Dropdown align="right" width="w-52">
        <Dropdown.Trigger
          // Icon-only: the name is what tells one row's menu from another,
          // for a test and for a screen reader alike.
          ariaLabel={`Actions for ${user.subject}`}
          className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          activeClassName="bg-accent text-foreground"
        >
          <span aria-hidden="true">⋯</span>
        </Dropdown.Trigger>
        <Dropdown.Menu className="py-1">
          <Dropdown.Item onClick={toggleDisabled}>
            <span className="px-3 py-1.5 text-xs font-medium">
              {user.disabled ? "Enable" : "Disable"}
            </span>
          </Dropdown.Item>
          <Dropdown.Item onClick={doReset}>
            <span className="px-3 py-1.5 text-xs font-medium">
              Reset password
            </span>
          </Dropdown.Item>
          <div className="my-0.5 h-px bg-border" />
          <Dropdown.Item onClick={() => setConfirmDelete(true)}>
            <span className="px-3 py-1.5 text-xs font-medium text-destructive">
              Delete
            </span>
          </Dropdown.Item>
        </Dropdown.Menu>
      </Dropdown>

      <Modal
        isOpen={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title="Delete user"
        description={`${label} loses access immediately. Audit entries they produced are kept.`}
        size="sm"
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={doDelete} disabled={busy}>
              {busy ? "Deleting…" : "Delete"}
            </Button>
          </div>
        }
      >
        <p className="text-sm text-muted-foreground">
          This cannot be undone from the console.
        </p>
      </Modal>
    </>
  );
}

/**
 * Shows a freshly generated password once. Deliberately a modal rather than
 * a notification: the server will not send this value again, so it has to be
 * dismissed on purpose.
 */
export function PasswordResetResult({
  target,
  onClose,
}: {
  target: ResetTarget | null;
  onClose: () => void;
}) {
  return (
    <Modal
      isOpen={target !== null}
      onClose={onClose}
      title="Temporary password"
      description={target ? `For ${target.subject}` : ""}
      size="sm"
      footer={
        <div className="flex justify-end">
          <Button onClick={onClose}>Done</Button>
        </div>
      }
    >
      <div className="space-y-3">
        <code className="block w-full rounded-md bg-muted px-3 py-2 font-mono text-sm break-all">
          {target?.password}
        </code>
        <p className="text-xs text-muted-foreground">
          Shown once. Give it to the user over a channel you trust and have them
          change it — the server keeps only its hash.
        </p>
      </div>
    </Modal>
  );
}
