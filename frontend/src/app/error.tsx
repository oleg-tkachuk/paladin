"use client";

import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";

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
      <h1 className="text-2xl font-bold text-white mb-3 tracking-tight">
        Something went wrong
      </h1>
      <p className="text-sm text-slate-400 text-center max-w-md mb-2">
        {error.message || "An unexpected error occurred."}
      </p>
      {error.digest && (
        <p className="text-xs font-mono text-slate-600 mb-8">
          Digest: {error.digest}
        </p>
      )}
      <button
        onClick={reset}
        className="px-6 py-3 rounded-2xl bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-bold transition-all shadow-lg shadow-indigo-600/20 active:scale-95"
      >
        Try Again
      </button>
    </div>
  );
}
