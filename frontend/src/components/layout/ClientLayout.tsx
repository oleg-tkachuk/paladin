"use client";

import React, { useState } from "react";
import { usePathname } from "next/navigation";

import { Sidebar } from "@/components/layout/Sidebar";
import { TopBar } from "@/components/layout/TopBar";
import { CommandPalette } from "@/components/layout/CommandPalette";
import { ActionsProvider } from "@/context/ActionsContext";
import { TenantProvider } from "@/context/TenantContext";
import { ScopeProvider } from "@/context/ScopeContext";
import { StatsProvider } from "@/context/StatsContext";
import { RefreshProvider } from "@/context/RefreshContext";

// Routes rendered without the sidebar/top-bar shell. Login is the obvious
// case; add other "logged-out" surfaces here (password reset, accept-invite)
// as they show up.
const STANDALONE_ROUTES = new Set<string>(["/login"]);

/**
 * Shell layout — uses **natural document scroll**.
 *
 * Earlier the shell wrapped everything in `h-screen overflow-hidden` and
 * gave only `<main>` `overflow-y-auto`. That isolates wheel events to the
 * `<main>` element only — it breaks scroll restoration, Cmd+Down, anchor
 * jumps, and on some browsers wheel-over-sidebar simply does nothing.
 *
 * The new layout lets <body> / <html> scroll naturally:
 *   - Sidebar pins itself with `sticky top-0 h-screen` (handled inside Sidebar)
 *   - TopBar is `sticky top-0 z-30` (handled inside TopBar)
 *   - `<main>` is just regular flow — no overflow trap
 */
export function ClientLayout({ children }: { children: React.ReactNode }) {
  const [isSidebarOpen, setSidebarOpen] = useState(false);
  const pathname = usePathname();

  if (STANDALONE_ROUTES.has(pathname)) {
    return <>{children}</>;
  }

  // Authed-only providers — fire RPCs (listTenants, getVersion, etc.) so
  // they must not mount on /login where there is no session yet.
  return (
    <RefreshProvider>
      <ActionsProvider>
        <TenantProvider>
          <ScopeProvider>
            <StatsProvider>
              <div className="flex min-h-screen w-full bg-background text-foreground">
                <CommandPalette />
                <Sidebar
                  isOpen={isSidebarOpen}
                  onClose={() => setSidebarOpen(false)}
                />
                <div className="flex min-w-0 flex-1 flex-col">
                  <TopBar onMenuToggle={() => setSidebarOpen((v) => !v)} />
                  <main className="flex-1">
                    <div className="mx-auto w-full max-w-[1600px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
                      {children}
                    </div>
                  </main>
                </div>
              </div>
            </StatsProvider>
          </ScopeProvider>
        </TenantProvider>
      </ActionsProvider>
    </RefreshProvider>
  );
}
