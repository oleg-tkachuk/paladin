"use client";

import { PageHeader } from "@/components/layout/PageHeader";
import { AdjustmentsHorizontalIcon } from "@heroicons/react/24/outline";

export default function GenericSystemPage({ title }: { title: string }) {
  return (
    <div className="space-y-8 animate-fade-in">
      <PageHeader title={title} />
      <div className="bg-surface/30 border border-white/5 rounded-xl p-12 text-center">
        <div className="w-16 h-16 bg-slate-500/10 rounded-full flex items-center justify-center mx-auto mb-4">
          <AdjustmentsHorizontalIcon className="w-8 h-8 text-slate-400" />
        </div>
        <h2 className="text-xl font-bold text-white mb-2">{title}</h2>
        <p className="text-slate-400 max-w-md mx-auto">
          This module is part of the system management suite. Functional
          implementation matching v1 is in progress.
        </p>
      </div>
    </div>
  );
}
