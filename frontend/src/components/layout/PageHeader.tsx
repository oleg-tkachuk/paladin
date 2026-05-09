"use client";

import React from "react";
import Link from "next/link";
import { ArrowUpTrayIcon, CubeIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { useScope } from "@/context/ScopeContext";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { Breadcrumbs } from "./Breadcrumbs";

interface PageHeaderProps {
  title: React.ReactNode;
  description?: string;
  /** @deprecated tenantId now comes from useScope(); prop kept for API back-compat. */
  tenantId?: string;
  actions?: React.ReactNode;
  showDefaultActions?: boolean;
  showBreadcrumbs?: boolean;
}

/**
 * Standard page header. Hierarchy:
 *
 *   Breadcrumbs (route)
 *   ── ── ── ── ── ── ── ── ── ── ── ── ── ── ── ──
 *   Title              [actions...] [defaultActions]
 *   Description
 *   Scope breadcrumb [backend] · [bucket] · [tenant]
 */
export const PageHeader: React.FC<PageHeaderProps> = ({
  title,
  description,
  tenantId: propTenantId,
  actions,
  showDefaultActions = true,
  showBreadcrumbs = true,
}) => {
  const { tenantId, tenant, backendId, bucketName, openScopePicker } =
    useScope();

  const tenantLabel =
    tenant?.displayName?.trim() ||
    propTenantId ||
    (tenantId ? `${tenantId.slice(0, 8)}…` : null);

  return (
    <div className="space-y-4">
      {showBreadcrumbs && <Breadcrumbs />}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1 space-y-2">
          {typeof title === "string" ? (
            <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground sm:text-3xl">
              {title}
            </h1>
          ) : (
            title
          )}
          {description && (
            <div
              className={cn(T.helper, "flex flex-wrap items-center gap-x-3")}
            >
              <span>{description}</span>
            </div>
          )}
          <ScopeBreadcrumb
            backendId={backendId}
            bucketName={bucketName}
            tenantLabel={tenantLabel}
            onOpen={openScopePicker}
          />
        </div>
        <div className="flex flex-wrap items-center gap-2 shrink-0">
          {actions}
          {showDefaultActions && (
            <>
              {actions && (
                <Separator
                  orientation="vertical"
                  className="hidden h-6 sm:block"
                />
              )}
              <Button variant="outline" size="sm" asChild>
                <Link href="/upload">
                  <ArrowUpTrayIcon className="size-4" />
                  Upload
                </Link>
              </Button>
              <Button size="sm" asChild>
                <Link href="/objects">
                  <CubeIcon className="size-4" />
                  Explore
                </Link>
              </Button>
            </>
          )}
        </div>
      </div>
      <Separator />
    </div>
  );
};

// ─── ScopeBreadcrumb ────────────────────────────────────────────────────────

interface ScopeBreadcrumbProps {
  backendId: string | null;
  bucketName: string | null;
  tenantLabel: string | null;
  onOpen: () => void;
}

/**
 * Three-segment breadcrumb [backend] · [bucket] · [tenant]. Each
 * segment is a Badge styled as a button — clicking any opens the
 * <ScopePicker> via the shared open state on ScopeContext. Empty
 * values render "—" so the breadcrumb shape stays stable across
 * pages. Backend + bucket segments hide on phones (md breakpoint)
 * — the tenant segment is always visible because every page is
 * tenant-scoped.
 */
function ScopeBreadcrumb({
  backendId,
  bucketName,
  tenantLabel,
  onOpen,
}: ScopeBreadcrumbProps) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <ScopeSegment
        label="backend"
        value={backendId}
        onOpen={onOpen}
        className="hidden md:inline-flex"
      />
      <span
        className={cn(T.hint, "hidden shrink-0 md:inline")}
        aria-hidden="true"
      >
        ·
      </span>
      <ScopeSegment
        label="bucket"
        value={bucketName}
        onOpen={onOpen}
        className="hidden md:inline-flex"
      />
      <span
        className={cn(T.hint, "hidden shrink-0 md:inline")}
        aria-hidden="true"
      >
        ·
      </span>
      <ScopeSegment label="tenant" value={tenantLabel} onOpen={onOpen} />
    </div>
  );
}

interface ScopeSegmentProps {
  label: string;
  value: string | null;
  onOpen: () => void;
  className?: string;
}

function ScopeSegment({ label, value, onOpen, className }: ScopeSegmentProps) {
  return (
    <Badge asChild variant="outline" className={cn("gap-1", className)}>
      <button
        type="button"
        onClick={onOpen}
        aria-label={`Change ${label} (currently ${value ?? "unset"})`}
        title={`Change ${label}`}
        className="cursor-pointer"
      >
        <span className={cn(T.labelTight)}>{label}</span>
        <span className={cn(T.codeSmall, value ? "" : "italic opacity-70")}>
          {value ?? "—"}
        </span>
      </button>
    </Badge>
  );
}
