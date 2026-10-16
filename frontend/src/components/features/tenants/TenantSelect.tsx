"use client";

import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import type { FormControlProps } from "@/components/ui/form-dialog";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";

/** What a tenant picker needs of a tenant. */
export type TenantOption = Pick<Tenant, "tenantId" | "slug" | "displayName">;

/** How a tenant reads in a picker: its display name, then its slug. */
export function tenantOptionLabel(t: TenantOption): string {
  if (t.displayName && t.slug && t.displayName !== t.slug) {
    return `${t.displayName} (${t.slug})`;
  }
  return t.displayName || t.slug || t.tenantId;
}

/**
 * A select over tenants, valued by tenant UUID — what the bucket owner and
 * grant RPCs take (ADR-0032). The bucket create dialog and the bucket page's
 * grant form share it.
 */
export function TenantSelect({
  value,
  onValueChange,
  tenants,
  placeholder,
  disabled = false,
  label,
}: {
  value: string;
  onValueChange: (tenantId: string) => void;
  tenants: TenantOption[];
  placeholder: string;
  disabled?: boolean;
  /**
   * What names the select: a FormField's control (its label points at the
   * id), or an aria-label where there is no visible label.
   */
  label: FormControlProps | { "aria-label": string };
}) {
  const named = "id" in label ? label : null;
  const ariaLabel = "aria-label" in label ? label["aria-label"] : undefined;
  return (
    <SelectRoot
      value={value}
      onValueChange={onValueChange}
      disabled={disabled || tenants.length === 0}
    >
      <SelectTrigger
        id={named?.id}
        aria-describedby={named?.["aria-describedby"]}
        aria-invalid={named?.["aria-invalid"]}
        aria-label={ariaLabel}
      >
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {tenants.map((t) => (
          <SelectItem key={t.tenantId} value={t.tenantId}>
            {tenantOptionLabel(t)}
          </SelectItem>
        ))}
      </SelectContent>
    </SelectRoot>
  );
}
