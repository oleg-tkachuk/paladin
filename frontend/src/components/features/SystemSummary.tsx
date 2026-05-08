"use client";

import React, { useEffect } from "react";
import { useTenants } from "@/hooks/useTenants";
import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useObjectTags } from "@/hooks/useObjectTags";
import { useSidebarCounts } from "@/hooks/useSidebarCounts";
import { Card } from "@/components/ui/Card";
import {
  UsersIcon,
  ArchiveBoxIcon,
  TagIcon,
  DocumentDuplicateIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { cn } from "@/lib/utils";

interface SummaryCardProps {
  title: string;
  value: number;
  icon: React.ElementType;
  color: "indigo" | "emerald" | "blue" | "rose" | "amber";
  loading?: boolean;
}

const SummaryCard = ({
  title,
  value,
  icon: Icon,
  color,
  loading,
}: SummaryCardProps) => {
  const colorClasses = {
    indigo:
      "text-indigo-400 bg-indigo-500/10 border-indigo-500/20 shadow-indigo-500/5",
    emerald:
      "text-emerald-400 bg-emerald-500/10 border-emerald-500/20 shadow-emerald-500/5",
    blue: "text-blue-400 bg-blue-500/10 border-blue-500/20 shadow-blue-500/5",
    rose: "text-rose-400 bg-rose-500/10 border-rose-500/20 shadow-rose-500/5",
    amber:
      "text-amber-400 bg-amber-500/10 border-amber-500/20 shadow-amber-500/5",
  };

  return (
    <Card
      variant="glass"
      className="p-6 border-white/5 hover:border-white/10 transition-all flex items-center gap-6 group"
    >
      <div
        className={cn(
          "w-14 h-14 rounded-2xl border flex items-center justify-center shrink-0 transition-transform group-hover:scale-110",
          colorClasses[color],
        )}
      >
        <Icon className="w-7 h-7" />
      </div>
      <div>
        <p className="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-1">
          {title}
        </p>
        <div className="flex items-baseline gap-2">
          {loading ? (
            <div className="h-8 w-16 bg-white/5 animate-pulse rounded-lg" />
          ) : (
            <h3 className="text-2xl font-bold text-white tracking-tight">
              {value.toLocaleString()}
            </h3>
          )}
        </div>
      </div>
    </Card>
  );
};

export const SystemSummary = () => {
  const { tenants, fetchTenants, loading: tenantsLoading } = useTenants();
  const {
    objectKeys,
    fetchObjectKeys,
    loading: objectKeysLoading,
  } = useObjectKeys();
  const {
    objectTags,
    fetchObjectTags,
    loading: objectTagsLoading,
  } = useObjectTags();
  const sidebar = useSidebarCounts();

  useEffect(() => {
    fetchTenants();
    fetchObjectKeys();
    fetchObjectTags();
  }, [fetchTenants, fetchObjectKeys, fetchObjectTags]);

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-6">
      <SummaryCard
        title="Tenants"
        value={tenants.length}
        icon={UsersIcon}
        color="indigo"
        loading={tenantsLoading}
      />
      <SummaryCard
        title="Buckets"
        value={objectKeys.length}
        icon={ArchiveBoxIcon}
        color="blue"
        loading={objectKeysLoading}
      />
      <SummaryCard
        title="Object Tags"
        value={objectTags.length}
        icon={TagIcon}
        color="emerald"
        loading={objectTagsLoading}
      />
      <SummaryCard
        title="Objects"
        value={sidebar.objects ?? 0}
        icon={DocumentDuplicateIcon}
        color="amber"
        loading={sidebar.objects === null}
      />
      <SummaryCard
        title="Trash Bin"
        value={sidebar.trash ?? 0}
        icon={TrashIcon}
        color="rose"
        loading={sidebar.trash === null}
      />
    </div>
  );
};
