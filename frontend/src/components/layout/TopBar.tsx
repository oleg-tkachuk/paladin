"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ArrowRightOnRectangleIcon,
  Bars3Icon,
  KeyIcon,
  MagnifyingGlassIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Separator } from "@/components/ui/separator";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { RealTimeStatus } from "@/components/features/RealTimeStatus";
import { BackgroundOpsDrawer } from "@/components/BackgroundOpsDrawer";
import { useAuth } from "@/context/AuthContext";
import { ScopePicker } from "@/components/layout/ScopePicker";

interface TopBarProps {
  onMenuToggle: () => void;
}

function monogramFor(name: string): string {
  const trimmed = (name || "").trim();
  if (!trimmed) return "—";
  return (
    trimmed
      .split(/\s+/)
      .slice(0, 2)
      .map((s) => s[0])
      .join("")
      .toUpperCase()
      .slice(0, 2) || "—"
  );
}

export function TopBar({ onMenuToggle }: TopBarProps) {
  const router = useRouter();
  const { user, logout } = useAuth();

  // The Cmd+K shortcut wires straight to the existing CommandPalette,
  // which listens for the keystroke globally — we just open the same
  // surface visually here.
  const openCommand = () => {
    const ev = new KeyboardEvent("keydown", {
      key: "k",
      metaKey: true,
      ctrlKey: true,
      bubbles: true,
    });
    window.dispatchEvent(ev);
  };

  const displayName = user?.displayName || user?.subject || "—";
  const initials = monogramFor(displayName);

  const handleLogout = async () => {
    try {
      await logout();
    } finally {
      router.push("/login");
    }
  };

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-3 border-b border-border bg-background/80 px-4 backdrop-blur-xl sm:px-6">
      <Button
        variant="ghost"
        size="icon"
        className="lg:hidden"
        onClick={onMenuToggle}
        aria-label="Toggle navigation"
      >
        <Bars3Icon className="size-5" />
      </Button>

      {/* Command-palette trigger */}
      <button
        type="button"
        onClick={openCommand}
        className="group inline-flex h-9 flex-1 max-w-md items-center gap-2 rounded-md border border-input bg-muted/40 px-3 text-left text-sm text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 focus-visible:outline-none"
      >
        <MagnifyingGlassIcon className="size-4 shrink-0" />
        <span className="truncate">Search anything…</span>
        <kbd className="ml-auto hidden items-center gap-0.5 rounded border bg-background px-1.5 font-mono text-[10px] font-medium text-muted-foreground sm:inline-flex">
          ⌘K
        </kbd>
      </button>

      <div className="ml-auto flex items-center gap-3">
        <ScopePicker />
        <Separator orientation="vertical" className="hidden h-6 sm:block" />
        <RealTimeStatus />
        <BackgroundOpsDrawer />
        <Separator orientation="vertical" className="h-6" />
        {/* Avatar → user menu. The avatar used to be a static fallback;
            now it's a dropdown trigger with shortcuts to /profile,
            /api-tokens, and the logout flow. */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label="Open user menu"
              className="rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <Avatar className="h-8 w-8">
                <AvatarFallback className="bg-primary text-primary-foreground font-semibold text-xs">
                  {initials}
                </AvatarFallback>
              </Avatar>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <DropdownMenuLabel>
              <div className="flex flex-col gap-0.5">
                <span className="truncate text-sm font-medium">
                  {displayName}
                </span>
                {user?.subject && user.subject !== displayName ? (
                  <span className="truncate font-mono text-[11px] text-muted-foreground">
                    {user.subject}
                  </span>
                ) : null}
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link href="/profile">
                <UserCircleIcon className="size-4" />
                Profile
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link href="/api-tokens">
                <KeyIcon className="size-4" />
                API Tokens
              </Link>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={handleLogout}>
              <ArrowRightOnRectangleIcon className="size-4" />
              Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
