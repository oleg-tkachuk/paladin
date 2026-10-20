"use client";

import React, { useState } from "react";
import { usePathname } from "next/navigation";

import { Sidebar } from "@/components/layout/Sidebar";
import { TopBar } from "@/components/layout/TopBar";
import { CommandPalette } from "@/components/layout/CommandPalette";
import { LoadingBar } from "@/components/layout/LoadingBar";
import { RecordVisits } from "@/components/layout/ResourceLinks";
import { OperationNotifier } from "@/components/OperationNotifier";
import { KeyboardHelp } from "@/components/KeyboardHelp";
import { AuthGate } from "@/components/AuthGate";
import { SettingsSync } from "@/components/SettingsSync";
import { AdminPlaneGate } from "@/components/AdminPlaneGate";
import { useGlobalShortcuts } from "@/hooks/useGlobalShortcuts";
import { ActionsProvider } from "@/context/ActionsContext";
import { ScopeProvider } from "@/context/ScopeContext";
import { ShellProvider } from "@/context/ShellContext";
import { StatsProvider } from "@/context/StatsContext";
import { RefreshProvider } from "@/context/RefreshContext";
import { UndoTrashProvider } from "@/hooks/useUndoTrash";
import { CrumbNamesProvider, useCrumbNames } from "@/context/CrumbNamesContext";

import { pageTitle } from "./crumbs";
import { PlatformBanner } from "./PlatformBanner";
import { ShellPageTitle } from "./ShellPageTitle";

// Routes that DON'T require an authenticated session — these render
// without the shell and bypass the AuthGate. Login is the canonical
// entry point; add password-reset / accept-invite here when they ship.
// Every other route in the app is gated: unauthenticated visits get
// redirected to /login?next=<originally-requested-url> before any
// chrome or RPC mounts.
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
/**
 * The g-prefix jumps. A component rather than a hook call in Shell: the
 * handler reads the scoped tenant, so it has to sit inside ScopeProvider,
 * and behind AuthGate so the login page has no jumps.
 */
function GlobalShortcuts() {
  useGlobalShortcuts();
  return null;
}

/** The document title, with the names the page gave its id segments. */
function PageTitle({ pathname }: { pathname: string }) {
  return <title>{pageTitle(pathname, useCrumbNames())}</title>;
}

export function ClientLayout({ children }: { children: React.ReactNode }) {
  return (
    <CrumbNamesProvider>
      <Shell>{children}</Shell>
    </CrumbNamesProvider>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  const [isSidebarOpen, setSidebarOpen] = useState(false);
  const pathname = usePathname();

  // Rendered here rather than as static metadata: every page is a client
  // component, so the root layout's one title used to name all of them, and
  // ten open tabs all read "Paladin". React hoists this into <head>.
  const title = <PageTitle pathname={pathname} />;

  if (STANDALONE_ROUTES.has(pathname)) {
    return (
      <>
        {title}
        {children}
      </>
    );
  }

  // Authed-only providers — fire RPCs (listTenants, getVersion, etc.) so
  // they must not mount on /login where there is no session yet. The
  // AuthGate inside the providers blocks rendering of children entirely
  // until auth status is "authenticated"; the providers themselves are
  // safe to construct (they don't fetch on mount, they fetch on first
  // child read), but we keep the gate INSIDE the providers so the
  // mounted page tree gets the contexts it needs the moment auth lands.
  return (
    <>
      <RefreshProvider>
        <ActionsProvider>
          <ScopeProvider>
            <ShellProvider>
              <ShellPageTitle pathname={pathname} />
              <StatsProvider>
                <UndoTrashProvider>
                  <AuthGate publicRoutes={STANDALONE_ROUTES}>
                    <div className="flex min-h-screen w-full bg-background text-foreground">
                      <LoadingBar />
                      <GlobalShortcuts />
                      <RecordVisits />
                      <OperationNotifier />
                      <CommandPalette />
                      <KeyboardHelp />
                      <Sidebar
                        isOpen={isSidebarOpen}
                        onClose={() => setSidebarOpen(false)}
                      />
                      <div className="flex min-w-0 flex-1 flex-col">
                        <TopBar
                          onMenuToggle={() => setSidebarOpen((v) => !v)}
                        />
                        <PlatformBanner edge="top" />
                        <main className="flex-1">
                          <div className="mx-auto w-full max-w-400 px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
                            <SettingsSync>
                              <AdminPlaneGate>{children}</AdminPlaneGate>
                            </SettingsSync>
                          </div>
                        </main>
                        <PlatformBanner edge="bottom" />
                      </div>
                    </div>
                  </AuthGate>
                </UndoTrashProvider>
              </StatsProvider>
            </ShellProvider>
          </ScopeProvider>
        </ActionsProvider>
      </RefreshProvider>
    </>
  );
}
