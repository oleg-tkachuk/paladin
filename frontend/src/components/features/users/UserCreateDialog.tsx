"use client";

import { useState } from "react";

import { Modal } from "@/components/ui/Modal";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useUserAdmin } from "@/hooks/useUserAdmin";
import { ASSIGNABLE_ROLES } from "@/constants/roles";

/** Server floor on initial_password (buf.validate, min_len = 12). */
const MIN_PASSWORD_LENGTH = 12;

interface Props {
  isOpen: boolean;
  onClose: () => void;
  onCreated: () => void;
  tenants: Array<{ tenantId: string; slug: string; displayName: string }>;
}

export function UserCreateDialog({
  isOpen,
  onClose,
  onCreated,
  tenants,
}: Props) {
  const { busy, createUser } = useUserAdmin();
  const [subject, setSubject] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [roles, setRoles] = useState<string[]>(["tenant.user"]);
  const [error, setError] = useState<string | null>(null);

  const tooShort = password.length > 0 && password.length < MIN_PASSWORD_LENGTH;
  const canSubmit =
    !busy &&
    subject.trim().length > 0 &&
    password.length >= MIN_PASSWORD_LENGTH;

  function toggleRole(role: string) {
    setRoles((prev) =>
      prev.includes(role) ? prev.filter((r) => r !== role) : [...prev, role],
    );
  }

  function reset() {
    setSubject("");
    setDisplayName("");
    setPassword("");
    setTenantId("");
    setRoles(["tenant.user"]);
    setError(null);
  }

  async function submit() {
    setError(null);
    const res = await createUser({
      // Empty parent means a platform-level user, which is what the proto
      // documents — not a missing value.
      parent: tenantId ? `tenants/${tenantId}` : "",
      subject: subject.trim(),
      displayName: displayName.trim(),
      initialPassword: password,
      roles,
    });
    if (!res.ok) {
      setError(res.error ?? "Could not create the user.");
      return;
    }
    reset();
    onCreated();
    onClose();
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={() => {
        reset();
        onClose();
      }}
      title="New user"
      description="Creates an account with an initial password the user is expected to change."
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!canSubmit}>
            {busy ? "Creating…" : "Create user"}
          </Button>
        </div>
      }
    >
      <div className="space-y-4">
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}

        <div className="space-y-2">
          <Label htmlFor="user-subject">Subject</Label>
          <Input
            id="user-subject"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder="alice@example.com"
            autoComplete="off"
          />
          <p className="text-xs text-muted-foreground">
            The stable identifier the user signs in with. Immutable.
          </p>
        </div>

        <div className="space-y-2">
          <Label htmlFor="user-display-name">Display name</Label>
          <Input
            id="user-display-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="Alice Example"
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="user-tenant">Tenant</Label>
          <select
            id="user-tenant"
            className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
            value={tenantId}
            onChange={(e) => setTenantId(e.target.value)}
          >
            <option value="">Platform-level (no tenant)</option>
            {tenants.map((t) => (
              <option key={t.tenantId} value={t.tenantId}>
                {t.displayName || t.slug}
              </option>
            ))}
          </select>
        </div>

        <div className="space-y-2">
          <Label htmlFor="user-password">Initial password</Label>
          <Input
            id="user-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={tooShort || undefined}
            aria-describedby="user-password-hint"
          />
          <p
            id="user-password-hint"
            className={
              tooShort
                ? "text-xs text-destructive"
                : "text-xs text-muted-foreground"
            }
          >
            At least {MIN_PASSWORD_LENGTH} characters. Shown to you once — the
            server does not send it again.
          </p>
        </div>

        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Roles</legend>
          <div className="grid grid-cols-2 gap-2">
            {ASSIGNABLE_ROLES.map((role) => (
              <label key={role} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={roles.includes(role)}
                  onChange={() => toggleRole(role)}
                />
                <span className="font-mono text-xs">{role}</span>
              </label>
            ))}
          </div>
        </fieldset>
      </div>
    </Modal>
  );
}
