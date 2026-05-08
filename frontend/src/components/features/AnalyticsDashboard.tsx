"use client";

import React from "react";
import {
  ServerIcon,
  CpuChipIcon,
  CodeBracketIcon,
  GlobeAltIcon,
} from "@heroicons/react/24/outline";
import { Skeleton } from "../ui/Skeleton";
import { Card } from "@/components/ui/Card";
import { cn } from "@/lib/utils";
import { useStats } from "@/context/StatsContext";
import { componentStatusLabel } from "@/lib/connect/system";

interface StatCardProps {
  title: string;
  value: string | number;
  icon: React.ElementType;
  color: "indigo" | "emerald" | "blue" | "rose";
  footerContent?: React.ReactNode;
}

const StatCard = React.memo(
  ({ title, value, icon: Icon, color, footerContent }: StatCardProps) => {
    const cardHoverBorder = {
      indigo: "hover:border-indigo-500/30",
      emerald: "hover:border-emerald-500/30",
      blue: "hover:border-blue-500/30",
      rose: "hover:border-rose-500/30",
    };

    const iconClasses = {
      indigo:
        "text-indigo-400 group-hover:bg-indigo-500 bg-indigo-500/10 border-indigo-500/20",
      emerald:
        "text-emerald-400 group-hover:bg-emerald-500 bg-emerald-500/10 border-emerald-500/20",
      blue: "text-blue-400 group-hover:bg-blue-500 bg-blue-500/10 border-blue-500/20",
      rose: "text-rose-400 group-hover:bg-rose-500 bg-rose-500/10 border-rose-500/20",
    };

    return (
      <Card
        variant="glass"
        className={cn("group transition-all py-4 px-6", cardHoverBorder[color])}
      >
        <div className="flex items-start justify-between mb-4">
          <div>
            <p className="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-2">
              {title}
            </p>
            <h3
              className="text-xl font-bold text-white tracking-tight truncate max-w-[120px]"
              title={String(value)}
            >
              {value}
            </h3>
          </div>
          <div
            className={cn(
              "w-8 h-8 rounded-lg border flex items-center justify-center transition-all",
              iconClasses[color],
            )}
          >
            <Icon className="w-4 h-4" />
          </div>
        </div>
        {footerContent}
      </Card>
    );
  },
);

StatCard.displayName = "StatCard";

export const AnalyticsDashboard: React.FC = () => {
  const { stats, loading, lastUpdated } = useStats();

  if (loading && !stats) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        {[...Array(4)].map((_, i) => (
          <Skeleton key={i} className="h-40 rounded-[2rem] px-6 py-8" />
        ))}
      </div>
    );
  }

  if (!stats) return null;

  const { version, health } = stats;
  const components = health?.components ?? [];
  const maxLatency = components.reduce(
    (max, c) => (Number(c.latencyMs) > max ? Number(c.latencyMs) : max),
    0,
  );
  const healthyCount = components.filter(
    (c) => componentStatusLabel(c.status) === "HEALTHY",
  ).length;
  const rollup =
    health?.status !== undefined
      ? componentStatusLabel(health.status)
      : "UNKNOWN";

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-slate-500">
          <div
            className={cn(
              "w-1.5 h-1.5 rounded-full",
              rollup === "HEALTHY"
                ? "bg-emerald-500"
                : "bg-rose-500 animate-pulse",
            )}
          />
          System Status: {rollup}
        </div>
        {lastUpdated && (
          <div className="text-xs font-medium text-slate-600 uppercase tracking-wider">
            Last Sync: {lastUpdated.toLocaleTimeString()}
          </div>
        )}
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        <StatCard
          title="Service Version"
          value={version?.version || "N/A"}
          icon={ServerIcon}
          color="indigo"
          footerContent={
            <div className="mt-4 flex flex-col gap-1">
              <div className="flex items-center justify-between text-xs font-semibold tracking-wider">
                <span className="text-slate-500 uppercase">Commit</span>
                <span className="text-indigo-400 font-mono lowercase">
                  {version?.commit?.substring(0, 7) || "N/A"}
                </span>
              </div>
            </div>
          }
        />

        <StatCard
          title="Rollup Status"
          value={rollup}
          icon={CpuChipIcon}
          color="emerald"
          footerContent={
            <div className="mt-4 flex items-center justify-between text-xs uppercase font-semibold tracking-wider">
              <span className="text-slate-500">Components</span>
              <span className="text-emerald-400">
                {healthyCount}/{components.length} healthy
              </span>
            </div>
          }
        />

        <StatCard
          title="Max Latency"
          value={`${maxLatency} ms`}
          icon={GlobeAltIcon}
          color="blue"
          footerContent={
            <div className="mt-4 flex items-center justify-between text-xs uppercase font-semibold tracking-wider">
              <span className="text-slate-500">Components</span>
              <span className="text-blue-400">{components.length}</span>
            </div>
          }
        />

        <StatCard
          title="Go Runtime"
          value={version?.goVersion || "N/A"}
          icon={CodeBracketIcon}
          color="rose"
          footerContent={
            <div className="mt-4 text-xs font-semibold tracking-wider">
              <span className="text-slate-500 uppercase block">Built</span>
              <span className="text-rose-400 font-mono lowercase truncate block">
                {version?.buildTime
                  ? new Date(
                      Number(version.buildTime.seconds) * 1000,
                    ).toLocaleString(undefined, {
                      month: "short",
                      day: "numeric",
                      hour: "2-digit",
                      minute: "2-digit",
                    })
                  : "dev"}
              </span>
            </div>
          }
        />
      </div>
    </div>
  );
};
