"use client";

/**
 * A user's scopes: what resources their roles reach. Each grant and revoke is
 * its own RPC and audit entry. A user with no scopes reaches whatever their
 * roles and tenant allow; a scope narrows that.
 */

import { useState } from "react";

import { Card } from "@/components/ui/Card";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { useUserAdmin } from "@/hooks/useUserAdmin";
import type { Scope } from "@/gen/paladin/common/v1/scope_pb";
import { ScopeType } from "@/gen/paladin/common/v1/scope_pb";
import {
  GRANTABLE_SCOPE_TYPES,
  SCOPE_VALUE_EXAMPLES,
  SCOPE_WILDCARD,
  scopeTypeName,
  scopeWire,
} from "@/lib/scopes";
import { T } from "@/lib/ui/typography";

interface UserForScopes {
  name: string;
  scopes: Pick<Scope, "type" | "value">[];
}

export function UserScopesCard({
  user,
  onChanged,
}: {
  user: UserForScopes;
  onChanged: () => void;
}) {
  const { busy, grantScopes, revokeScopes } = useUserAdmin();
  const { showNotification } = useNotification();
  const [type, setType] = useState<ScopeType>(ScopeType.BUCKET);
  const [value, setValue] = useState("");
  // Revoking narrows nothing: it takes a limit away. The last one leaves the
  // user unscoped, reaching all their roles allow, so it always asks first.
  const [revoking, setRevoking] = useState<Pick<
    Scope,
    "type" | "value"
  > | null>(null);

  const trimmed = value.trim();
  const held = user.scopes.some((s) => s.type === type && s.value === trimmed);
  const grantBlocked = !trimmed
    ? "Enter a value to grant."
    : held
      ? "The user already holds this scope."
      : null;

  async function grant() {
    const scope = { type, value: trimmed };
    const res = await grantScopes(user.name, [scope]);
    if (!res.ok) {
      showNotification({
        type: "error",
        title: "Could not grant the scope",
        message: res.error ?? "",
      });
      return;
    }
    showNotification({
      type: "success",
      title: "Scope granted",
      message: scopeWire(scope),
    });
    setValue("");
    onChanged();
  }

  async function revoke(scope: Pick<Scope, "type" | "value">) {
    const res = await revokeScopes(user.name, [scope]);
    if (!res.ok) {
      showNotification({
        type: "error",
        title: "Could not revoke the scope",
        message: res.error ?? "",
      });
      return;
    }
    showNotification({
      type: "success",
      title: "Scope revoked",
      message: scopeWire(scope),
    });
    onChanged();
  }

  return (
    <Card className="space-y-4 p-5">
      <div className="space-y-1">
        <h2 className="text-sm font-semibold">Scopes</h2>
        <p className={T.hint}>
          Narrow what the user&apos;s roles reach. Tokens already issued keep
          their scopes until they expire.
        </p>
      </div>

      {user.scopes.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No scopes: the roles reach everything the tenant allows.
        </p>
      ) : (
        <ul className="divide-y rounded-md border">
          {user.scopes.map((s) => {
            const wire = scopeWire(s);
            return (
              <li
                key={wire}
                className="flex items-center justify-between gap-3 px-3 py-2"
              >
                <span className="min-w-0 break-all font-mono text-xs">
                  {wire}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  aria-label={`Revoke ${wire}`}
                  onClick={() => setRevoking(s)}
                >
                  Revoke
                </Button>
              </li>
            );
          })}
        </ul>
      )}

      <ConfirmModal
        isOpen={revoking !== null}
        onClose={() => setRevoking(null)}
        onConfirm={async () => {
          if (revoking) await revoke(revoking);
          setRevoking(null);
        }}
        title="Revoke scope"
        message={
          revoking === null
            ? ""
            : user.scopes.length === 1
              ? `${scopeWire(revoking)} is the user's last scope. Without it they are unscoped: their roles reach everything the tenant allows.`
              : `The user loses ${scopeWire(revoking)}; their other scopes still limit them.`
        }
        confirmText="Revoke"
        loading={busy}
      />

      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (!grantBlocked) void grant();
        }}
      >
        <label className="space-y-1">
          <span className={T.label}>Type</span>
          <SelectRoot
            value={String(type)}
            onValueChange={(v) => setType(Number(v) as ScopeType)}
          >
            <SelectTrigger aria-label="Scope type" className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {GRANTABLE_SCOPE_TYPES.map((t) => (
                <SelectItem key={t} value={String(t)}>
                  {scopeTypeName(t)}
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        </label>
        <label className="min-w-48 flex-1 space-y-1">
          <span className={T.label}>Value</span>
          <Input
            aria-label="Scope value"
            className="font-mono text-xs"
            placeholder={`${SCOPE_VALUE_EXAMPLES[type]}, or ${SCOPE_WILDCARD}`}
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </label>
        <Button
          type="submit"
          disabled={Boolean(grantBlocked) || busy}
          title={grantBlocked ?? undefined}
        >
          Grant
        </Button>
      </form>
    </Card>
  );
}
