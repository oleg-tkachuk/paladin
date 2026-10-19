"use client";

import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";
import { PRESSED } from "@/lib/ui/states";
import { cn } from "@/lib/utils";

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] animate-fade-in px-6">
      <div className="w-20 h-20 rounded-3xl bg-destructive/10 border border-destructive/20 flex items-center justify-center mb-8">
        <ExclamationTriangleIcon className="w-10 h-10 text-destructive" />
      </div>
      <h1 className="text-2xl font-bold text-foreground mb-3 tracking-tight">
        Something went wrong
      </h1>
      <p className="text-sm text-muted-foreground text-center max-w-md mb-2">
        {error.message || "An unexpected error occurred."}
      </p>
      {error.digest && (
        <p className="text-xs font-mono text-muted-foreground mb-8">
          Digest: {error.digest}
        </p>
      )}
      <button
        onClick={reset}
        className={cn(
          "px-6 py-3 rounded-2xl bg-primary hover:bg-primary text-primary-foreground text-sm font-bold transition-all shadow-raised",
          PRESSED,
        )}
      >
        Try Again
      </button>
    </div>
  );
}
