import type { Metadata } from "next";
import React from "react";
// Self-hosted Geist via the `geist` npm package — the font binaries ship
// inside the package, so the build does NOT reach out to fonts.googleapis
// (unlike `next/font/google`, which fetches at build time). Same fonts
// and the same `--font-geist-sans` / `--font-geist-mono` CSS variables,
// fully air-gap-buildable.
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";

import { ClientLayout } from "@/components/layout/ClientLayout";
import { QueryProvider } from "@/components/providers/QueryProvider";
import { NotificationProvider } from "@/components/ui/Notification";
import { AuthProvider } from "@/context/AuthContext";
import { TooltipProvider } from "@/components/ui/Tooltip";
import { Toaster } from "@/components/ui/sonner";
import "./globals.css";

// No title here: ClientLayout writes one per route (see crumbs.ts), and a
// second <title> from metadata would come first in <head> and win.
export const metadata: Metadata = {
  description: "Control plane for object storage",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      lang="en"
      className={`${GeistSans.variable} ${GeistMono.variable} dark`}
      suppressHydrationWarning
    >
      <body className="bg-background text-foreground font-sans min-h-screen selection:bg-primary/30">
        {/*
          Provider stack order:
          - Tooltip / Notification / Auth at root: cheap, route-agnostic, and
            /login itself uses notifications + auth.
          - Tenant/Scope/Stats/Actions are mounted INSIDE ClientLayout so they
            never fire RPCs on standalone routes (/login). Otherwise their
            useEffect-driven list/version fetches throw before there's a
            session — visible as red toasts on the login page.
        */}
        <QueryProvider>
          <TooltipProvider delayDuration={150}>
            <NotificationProvider>
              <AuthProvider>
                <ClientLayout>{children}</ClientLayout>
                <Toaster richColors position="bottom-right" />
              </AuthProvider>
            </NotificationProvider>
          </TooltipProvider>
        </QueryProvider>
      </body>
    </html>
  );
}
