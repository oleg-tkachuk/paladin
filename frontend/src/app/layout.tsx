import type { Metadata } from "next";
import React from "react";
import { Geist, Geist_Mono } from "next/font/google";

import { ClientLayout } from "@/components/layout/ClientLayout";
import { NotificationProvider } from "@/components/ui/Notification";
import { AuthProvider } from "@/context/AuthContext";
import { TooltipProvider } from "@/components/ui/Tooltip";
import { Toaster } from "@/components/ui/sonner";
import { yamlConfig } from "@/config";
import "./globals.css";

const geistSans = Geist({
  subsets: ["latin"],
  variable: "--font-geist-sans",
  display: "swap",
});

const geistMono = Geist_Mono({
  subsets: ["latin"],
  variable: "--font-geist-mono",
  display: "swap",
});

export const metadata: Metadata = {
  title: "PALADIN — Paladin",
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
      className={`${geistSans.variable} ${geistMono.variable} dark`}
      suppressHydrationWarning
    >
      <head>
        {/* Dev-only fallback JWT injected from configs/config.yaml. React
            HTML-escapes attributes, so there's no XSS surface here. */}
        <meta
          name="paladin-dev-token"
          content={yamlConfig.runtimeConfig.public.auth.devToken}
        />
      </head>
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
        <TooltipProvider delayDuration={150}>
          <NotificationProvider>
            <AuthProvider>
              <ClientLayout>{children}</ClientLayout>
              <Toaster richColors position="bottom-right" />
            </AuthProvider>
          </NotificationProvider>
        </TooltipProvider>
      </body>
    </html>
  );
}
