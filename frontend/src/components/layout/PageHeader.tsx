"use client";

import React from "react";
import Link from "next/link";
import { ArrowUpTrayIcon, CubeIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { Breadcrumbs } from "./Breadcrumbs";
import { ScopeBadge } from "./ScopeBadge";

interface PageHeaderProps {
  title: React.ReactNode;
  description?: string;
  actions?: React.ReactNode;
  /**
   * Inject the standard Upload + Explore buttons. Defaults to `false`:
   * the primary actions live in the shell (sidebar Upload, Cmd+K), so a
   * page opts in only when these are genuinely its main affordance.
   */
  showDefaultActions?: boolean;
  showBreadcrumbs?: boolean;
}

/**
 * Standard page header.
 *
 *   Breadcrumbs (route location)                 [scope]
 *   Title                                        [actions…]
 *   Description
 *
 * The scope badge names the space the page is in — the platform, or the
 * tenant whose workspace it is (ADR-0033). That is not the session's tenant,
 * which the top bar's switcher shows: a platform admin acts on other tenants.
 */
export const PageHeader: React.FC<PageHeaderProps> = ({
  title,
  description,
  actions,
  showDefaultActions = false,
  showBreadcrumbs = true,
}) => {
  return (
    <div className="space-y-4">
      {showBreadcrumbs && (
        <div className="flex items-center justify-between gap-3">
          <Breadcrumbs />
          <ScopeBadge />
        </div>
      )}
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
            <p className={cn(T.helper, "max-w-2xl")}>{description}</p>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
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
              {/* Explore targets the resource gateway (/tenants); the flat
                  cross-tenant /objects list no longer exists. */}
              <Button size="sm" asChild>
                <Link href="/tenants">
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
