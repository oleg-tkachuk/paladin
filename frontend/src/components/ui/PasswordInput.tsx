"use client";

import * as React from "react";
import { EyeIcon, EyeSlashIcon } from "@heroicons/react/24/outline";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * A password field the user can reveal while typing: a long password typed
 * blind is where a mistype that locks the user out comes from. Hidden again
 * on every mount, never remembered.
 */
export function PasswordInput({
  className,
  ...props
}: Omit<React.ComponentProps<"input">, "type">) {
  const [shown, setShown] = React.useState(false);
  return (
    <div className="relative">
      <Input
        {...props}
        type={shown ? "text" : "password"}
        className={cn("pr-9", className)}
      />
      <button
        type="button"
        onClick={() => setShown((v) => !v)}
        aria-label={shown ? "Hide password" : "Show password"}
        aria-pressed={shown}
        className="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-muted-foreground hover:text-foreground"
      >
        {shown ? (
          <EyeSlashIcon className="size-4" />
        ) : (
          <EyeIcon className="size-4" />
        )}
      </button>
    </div>
  );
}
