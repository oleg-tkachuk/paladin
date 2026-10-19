"use client";

/**
 * useUserAdmin — the write half of iam/v1.UserService.
 *
 * The console has listed users since the page was written and could do
 * nothing else with them: no create, no role change, no disable, no delete,
 * no password reset. Seven of the service's eight RPCs had no caller, so
 * adding a colleague to a deployment meant an operator with a database
 * connection. This closes that.
 *
 * Errors are returned to the caller rather than swallowed into a toast —
 * the dialogs surface field-level messages, and only the page decides what
 * a failure looks like.
 */

import { useCallback, useState } from "react";

import type { User } from "@/gen/paladin/iam/v1/types_pb";
import { useOptimistic } from "@/hooks/useOptimistic";
import { edit, type OptimisticEdit } from "@/lib/optimistic";
import { USERS_LIST_QUERY_KEY, userDetailQuery } from "@/lib/queries/details";

import { userClient } from "@/lib/connect/client";
import { useBumpRefresh } from "@/context/RefreshContext";
import { errorMessage } from "@/hooks/errorContract";
import { fieldMask, type MaskField } from "@/lib/connect/fieldMask";
import { UpdateUserRequestSchema } from "@/gen/paladin/iam/v1/user_service_pb";
import type { Scope } from "@/gen/paladin/common/v1/scope_pb";

/** Resource name for a user: "users/{subject}" or, when tenant-scoped,
 *  "tenants/{tenant}/users/{subject}". The server accepts the name the
 *  List response carried, so callers pass that through unchanged. */
export interface UserAdminResult {
  ok: boolean;
  error?: string;
}

function toMessage(e: unknown): string {
  return errorMessage(e, "Unexpected error.");
}

/** The fields an update sets; the rest of the user is left as it is. */
export interface UserChange {
  displayName?: string;
  disabled?: boolean;
  roles?: string[];
}

/** The fields of `change` that are set, so an absent one changes nothing. */
function setFields(change: UserChange): Partial<User> {
  return Object.fromEntries(
    Object.entries(change).filter(([, v]) => v !== undefined),
  ) as Partial<User>;
}

/** An update as the list and the user's page show it before the answer. */
export function userChangeEdits(
  name: string,
  change: UserChange,
): OptimisticEdit[] {
  const fields = setFields(change);
  const changed = (u: User): User =>
    u.name === name ? { ...u, ...fields } : u;
  return [
    edit<User[]>(USERS_LIST_QUERY_KEY, (users) => users.map(changed)),
    edit<User>(userDetailQuery(name).queryKey, changed),
  ];
}

/** A delete as the list shows it before the answer. */
export function userRemovalEdits(name: string): OptimisticEdit[] {
  return [
    edit<User[]>(USERS_LIST_QUERY_KEY, (users) =>
      users.filter((u) => u.name !== name),
    ),
  ];
}

export function useUserAdmin() {
  const bumpRefresh = useBumpRefresh();
  const [busy, setBusy] = useState(false);

  const optimistic = useOptimistic();

  // `edits` show the change at once, and are put back if it is refused.
  const run = useCallback(
    async (
      fn: () => Promise<unknown>,
      edits: readonly OptimisticEdit[] = [],
    ): Promise<UserAdminResult> => {
      setBusy(true);
      try {
        await optimistic(edits, fn);
        bumpRefresh("users");
        return { ok: true };
      } catch (e) {
        return { ok: false, error: toMessage(e) };
      } finally {
        setBusy(false);
      }
    },
    [bumpRefresh, optimistic],
  );

  const createUser = useCallback(
    (args: {
      /** "tenants/{id}" — empty for a platform-level user. */
      parent: string;
      subject: string;
      displayName: string;
      initialPassword: string;
      roles: string[];
    }) =>
      run(() =>
        userClient.createUser({
          parent: args.parent,
          subject: args.subject,
          displayName: args.displayName,
          initialPassword: args.initialPassword,
          roles: args.roles,
        }),
      ),
    [run],
  );

  /**
   * Partial update. The field mask is what makes it partial: without it the
   * server cannot tell "leave roles alone" from "set roles to empty", and
   * an edit of the display name would silently strip every role.
   */
  const updateUser = useCallback(
    (args: {
      name: string;
      resourceVersion: string;
      displayName?: string;
      disabled?: boolean;
      roles?: string[];
    }) => {
      const paths: MaskField<typeof UpdateUserRequestSchema>[] = [];
      if (args.displayName !== undefined) paths.push("displayName");
      if (args.disabled !== undefined) paths.push("disabled");
      if (args.roles !== undefined) paths.push("roles");
      return run(
        () =>
          userClient.updateUser({
            name: args.name,
            resourceVersion: args.resourceVersion,
            updateMask: fieldMask(UpdateUserRequestSchema, ...paths),
            displayName: args.displayName ?? "",
            disabled: args.disabled ?? false,
            roles: args.roles ?? [],
          }),
        userChangeEdits(args.name, {
          displayName: args.displayName,
          disabled: args.disabled,
          roles: args.roles,
        }),
      );
    },
    [run],
  );

  const deleteUser = useCallback(
    (args: { name: string; resourceVersion: string }) =>
      run(
        () =>
          userClient.deleteUser({
            name: args.name,
            resourceVersion: args.resourceVersion,
          }),
        userRemovalEdits(args.name),
      ),
    [run],
  );

  /**
   * Returns the temporary password the server minted. It is shown once and
   * never stored — the caller is responsible for putting it in front of the
   * operator before the dialog closes.
   */
  const resetPassword = useCallback(
    async (
      name: string,
    ): Promise<{ ok: boolean; password?: string; error?: string }> => {
      setBusy(true);
      try {
        const res = await userClient.resetPassword({ name });
        bumpRefresh("users");
        return { ok: true, password: res.generatedPassword };
      } catch (e) {
        return { ok: false, error: toMessage(e) };
      } finally {
        setBusy(false);
      }
    },
    [bumpRefresh],
  );

  /**
   * Scopes have RPCs of their own rather than a field on UpdateUser, so each
   * grant and revoke is its own audit entry. Tokens already issued keep what
   * they carry until they expire; this changes what the next one carries.
   */
  const grantScopes = useCallback(
    (name: string, scopes: Pick<Scope, "type" | "value">[]) =>
      run(() => userClient.grantScopes({ name, scopes })),
    [run],
  );

  const revokeScopes = useCallback(
    (name: string, scopes: Pick<Scope, "type" | "value">[]) =>
      run(() => userClient.revokeScopes({ name, scopes })),
    [run],
  );

  return {
    busy,
    createUser,
    updateUser,
    deleteUser,
    resetPassword,
    grantScopes,
    revokeScopes,
  };
}
