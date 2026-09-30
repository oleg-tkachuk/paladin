"use client";

import type { ReactNode } from "react";
import { usePathname } from "next/navigation";

import { NoAdminRole } from "@/components/NoAdminRole";
import { canUseAdminPlane } from "@/constants/roles";
import { useAuth } from "@/context/AuthContext";
import { routeNeedsAdminPlane } from "@/lib/adminPlaneRoutes";

/**
 * Stands in for an admin view reached by URL, a bookmark or a shortcut by a
 * principal IAM will not issue the admin audience: the page would mount and
 * fail request by request. Until the user is known, the page renders — an
 * operator's view must not flash this on load.
 */
export function AdminPlaneGate({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const { user } = useAuth();
  if (user && !canUseAdminPlane(user.roles) && routeNeedsAdminPlane(pathname)) {
    return <NoAdminRole />;
  }
  return <>{children}</>;
}
