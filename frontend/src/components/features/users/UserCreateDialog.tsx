"use client";

import { useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { type FailedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { Input } from "@/components/ui/input";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import {
  FormDialog,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { useUserAdmin } from "@/hooks/useUserAdmin";
import {
  ADMIN_AUDIENCE_ROLES,
  ASSIGNABLE_ROLES,
  ROLE_DESCRIPTIONS,
  ROLES,
} from "@/constants/roles";

/** Server floor on initial_password (buf.validate, min_len = 12). */
const MIN_PASSWORD_LENGTH = 12;

/** The empty-parent option: CreateUser puts the user in the caller's tenant. */
export const OWN_TENANT_LABEL = "Your own tenant";

// Radix Select cannot carry an empty value; this stands for "no parent".
const OWN_TENANT_VALUE = "__own__";

interface Props {
  isOpen: boolean;
  onClose: () => void;
  onCreated: () => void;
  tenants: Array<{ tenantId: string; slug: string; displayName: string }>;
  /** Set when the tenant list failed to load: `tenants` is then unknown. */
  tenantsFailed?: FailedRead | null;
}

export function UserCreateDialog({
  isOpen,
  onClose,
  onCreated,
  tenants,
  tenantsFailed = null,
}: Props) {
  const { busy, createUser } = useUserAdmin();
  const [subject, setSubject] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [roles, setRoles] = useState<string[]>([ROLES.tenantUser]);
  const [error, setError] = useState<string | null>(null);

  const tooShort = password.length > 0 && password.length < MIN_PASSWORD_LENGTH;
  const blockedReason = !subject.trim()
    ? "Enter a subject to continue."
    : password.length < MIN_PASSWORD_LENGTH
      ? `The password needs at least ${MIN_PASSWORD_LENGTH} characters.`
      : roles.length === 0
        ? "Pick at least one role."
        : null;

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
    setRoles([ROLES.tenantUser]);
    setError(null);
  }

  function close() {
    reset();
    onClose();
  }

  async function submit() {
    setError(null);
    const res = await createUser({
      // Empty parent is the caller's own tenant, as the proto documents:
      // every user belongs to exactly one tenant.
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
    <FormDialog
      open={isOpen}
      onOpenChange={(o) => {
        if (!o) close();
      }}
      title="New user"
      description="An account with an initial password the user then changes."
      width="lg"
      onSubmit={() => void submit()}
      submitLabel="Create user"
      submittingLabel="Creating…"
      submitting={busy}
      blockedReason={blockedReason}
      error={error}
    >
      <FormSection title="Account">
        <FormRow>
          <FormField
            label="Subject"
            required
            hint="What the user signs in with. Cannot be changed later."
          >
            {(control) => (
              <Input
                {...control}
                autoFocus
                placeholder="alice@example.com"
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
              />
            )}
          </FormField>
          <FormField label="Display name">
            {(control) => (
              <Input
                {...control}
                placeholder="Alice Example"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
        {/* Creating in the operator's own tenant still works, so nothing is
            held — but the select offering only that one is not the full list,
            and should not read as if it were. */}
        {tenantsFailed ? (
          <ListLoadError
            variant="inline"
            what="Tenants"
            reason={tenantsFailed.reason}
            onRetry={tenantsFailed.retry}
          />
        ) : null}
        <FormRow>
          <FormField label="Tenant">
            {(control) => (
              <SelectRoot
                value={tenantId || OWN_TENANT_VALUE}
                onValueChange={(v) =>
                  setTenantId(v === OWN_TENANT_VALUE ? "" : v)
                }
              >
                <SelectTrigger {...control}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={OWN_TENANT_VALUE}>
                    {OWN_TENANT_LABEL}
                  </SelectItem>
                  {tenants.map((t) => (
                    <SelectItem key={t.tenantId} value={t.tenantId}>
                      {t.displayName || t.slug}
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            )}
          </FormField>
          <FormField
            label="Initial password"
            required
            error={
              tooShort ? `At least ${MIN_PASSWORD_LENGTH} characters.` : null
            }
            hint="Pass it to the user; it cannot be read back."
          >
            {(control) => (
              <Input
                {...control}
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Roles">
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
                  checked={roles.includes(role)}
                  onCheckedChange={() => toggleRole(role)}
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
      </FormSection>
    </FormDialog>
  );
}
