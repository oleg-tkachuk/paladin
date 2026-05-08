"use client";

import {
  ArrowPathIcon,
  ShieldCheckIcon,
  ClockIcon,
} from "@heroicons/react/24/outline";
import { cn } from "@/lib/utils";
import { useStats } from "@/context/StatsContext";
import { Tooltip } from "@/components/ui/Tooltip";
import {
  type ComponentHealth,
  componentStatusLabel,
} from "@/lib/connect/system";

function StatusChip({
  icon: Icon,
  label,
  value,
  color,
}: {
  icon: React.ElementType;
  label: string;
  value: string;
  color: string;
}) {
  return (
    <Tooltip content={label}>
      <div className="flex items-center gap-1.5">
        <Icon className={cn("w-3.5 h-3.5", color)} />
        <span className="text-xs font-mono font-semibold text-slate-300">
          {value}
        </span>
      </div>
    </Tooltip>
  );
}

export function RealTimeStatus() {
  const { stats, loading: isLoading } = useStats();

  const health = stats?.health;
  const version = stats?.version;

  const rollupStatus =
    health?.status !== undefined ? componentStatusLabel(health.status) : "";
  const connected = rollupStatus === "HEALTHY";

  const latency =
    health?.components?.reduce((max: number, dep: ComponentHealth) => {
      const l = Number(dep.latencyMs);
      return l > max ? l : max;
    }, 0) || 0;

  const componentCount = health?.components?.length || 0;

  return (
    <div className="hidden sm:flex items-center gap-3 px-3 py-1.5 rounded-xl bg-surface/30 border border-white/5 text-xs">
      <Tooltip
        content={
          connected
            ? "Backend connected"
            : `Status: ${rollupStatus || "unknown"}`
        }
      >
        <div className="flex items-center gap-1.5">
          <div className="relative">
            <div
              className={cn(
                "w-2 h-2 rounded-full",
                connected ? "bg-emerald-500 animate-pulse" : "bg-rose-500",
              )}
            />
          </div>
          <span className="text-xs font-semibold text-slate-500 uppercase tracking-wider">
            {connected ? "Live" : "Err"}
          </span>
        </div>
      </Tooltip>

      <div className="w-px h-3.5 bg-white/10" />

      <StatusChip
        icon={isLoading ? ArrowPathIcon : ArrowPathIcon}
        label="Max Component Latency"
        value={`${latency}ms`}
        color={cn("text-emerald-400", isLoading && "animate-spin")}
      />

      <div className="w-px h-3.5 bg-white/10 hidden md:block" />

      <div className="hidden md:block">
        <StatusChip
          icon={ShieldCheckIcon}
          label="Components Reporting"
          value={`${componentCount}`}
          color="text-amber-400"
        />
      </div>

      <div className="w-px h-3.5 bg-white/10 hidden lg:block" />

      <div className="hidden lg:block">
        <StatusChip
          icon={ClockIcon}
          label="Version"
          value={version?.version || "dev"}
          color="text-sky-400"
        />
      </div>
    </div>
  );
}
