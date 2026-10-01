"use client";

import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { planeTone, useMcpLive } from "./mcp-status";
import { errorMessage } from "@/hooks/errorContract";

function Tile({
  label,
  children,
  hint,
}: {
  label: string;
  children: React.ReactNode;
  hint?: React.ReactNode;
}) {
  return (
    <Card className="space-y-1.5 p-4">
      <div className={T.label}>{label}</div>
      <div className="flex min-h-7 items-center gap-2 text-lg font-semibold">
        {children}
      </div>
      {hint ? <div className={T.hint}>{hint}</div> : null}
    </Card>
  );
}

/**
 * The questions an operator opens this page with: is the MCP server up, can it
 * reach the planes, is anyone connected, and what can a connected agent do.
 */
export function MCPOverview({
  inspect,
}: {
  inspect: MCPInspectResponse | null;
}) {
  const { status, sessions } = useMcpLive();
  const s = status.data;
  const reason = status.error
    ? errorMessage(status.error)
    : s && !s.reachable
      ? s.error || "no answer"
      : null;

  const httpProfile = inspect?.transports?.http?.profile;
  const exposed = inspect?.profiles.find((p) => p.name === httpProfile)?.tools
    .length;

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <Tile
        label="Server"
        hint={reason ?? "Answering on the admin plane's behalf."}
      >
        {status.isPending ? (
          <Skeleton className="h-6 w-24" />
        ) : reason ? (
          <span className="flex items-center gap-1.5 text-destructive">
            <XCircleIcon className="size-5" /> Unreachable
          </span>
        ) : (
          <span className="flex items-center gap-1.5 text-success">
            <CheckCircleIcon className="size-5" /> Up
          </span>
        )}
      </Tile>

      <Tile
        label="Planes from the server"
        hint="Whether the MCP server itself reaches each plane."
      >
        {status.isPending ? (
          <Skeleton className="h-6 w-40" />
        ) : s && s.upstreams.length > 0 ? (
          <span className="flex flex-wrap gap-1.5">
            {s.upstreams.map((u) => (
              <Badge
                key={u.name}
                variant={u.reachable ? planeTone(u.name) : "destructive"}
                className={T.labelTight}
                title={u.url}
              >
                {u.reachable ? null : (
                  <ExclamationTriangleIcon className="size-3" />
                )}
                {u.name}
              </Badge>
            ))}
          </span>
        ) : (
          <span className="text-sm text-muted-foreground">Unknown</span>
        )}
      </Tile>

      <Tile
        label="Live sessions"
        hint={
          reason && !sessions.data?.sessions.length
            ? "Unknown while the server is unreachable."
            : "Streamable-HTTP sessions open now."
        }
      >
        {sessions.isPending ? (
          <Skeleton className="h-6 w-10" />
        ) : reason && !sessions.data?.sessions.length ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <span className="tabular-nums">
            {sessions.data?.sessions.length ?? 0}
          </span>
        )}
      </Tile>

      <Tile
        label="Agents on HTTP see"
        hint={
          httpProfile ? (
            <>
              Profile <span className={T.code}>{httpProfile}</span>
            </>
          ) : (
            "Profile unknown"
          )
        }
      >
        {inspect ? (
          <span className="tabular-nums">
            {exposed ?? "—"}
            <span className="text-sm font-normal text-muted-foreground">
              {" "}
              of {inspect.toolCatalog.length} tools
            </span>
          </span>
        ) : (
          <Skeleton className="h-6 w-24" />
        )}
      </Tile>
    </div>
  );
}

/**
 * How an agent connects, including the limit that surprises people: a login
 * token is issued for one plane, so it reaches only that plane's tools.
 */
export function MCPConnect({ inspect }: { inspect: MCPInspectResponse }) {
  const http = inspect.transports?.http;
  const stdio = inspect.transports?.stdio;
  return (
    <Card className="space-y-3 p-4">
      <h3 className={T.cardTitleProse}>Connecting an agent</h3>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="space-y-1.5 text-sm">
          <div className="flex items-center gap-2">
            <span className="font-medium">Streamable HTTP</span>
            <Badge
              variant={http?.enabled ? "success" : "outline"}
              className={T.labelTight}
            >
              {http?.enabled ? "enabled" : "disabled"}
            </Badge>
          </div>
          <p className={T.hint}>
            <span className={T.code}>POST {http?.addr || ":8095"}/mcp</span>{" "}
            with <span className={T.code}>Authorization: Bearer …</span>.
            Sessions end after{" "}
            {http?.sessionTimeoutSeconds
              ? `${http.sessionTimeoutSeconds}s`
              : "no set time"}{" "}
            idle and belong to the principal that opened them.
          </p>
        </div>
        <div className="space-y-1.5 text-sm">
          <div className="flex items-center gap-2">
            <span className="font-medium">stdio</span>
            <Badge
              variant={stdio?.enabled ? "success" : "outline"}
              className={T.labelTight}
            >
              {stdio?.enabled ? "enabled" : "disabled"}
            </Badge>
          </div>
          <p className={T.hint}>
            <span className={T.code}>paladin serve mcp --transport stdio</span>{" "}
            with the token in <span className={T.code}>PALADIN_MCP_TOKEN</span>
            {stdio?.profile ? (
              <>
                ; profile <span className={T.code}>{stdio.profile}</span>
              </>
            ) : null}
            .
          </p>
        </div>
      </div>
      <p
        className={cn(
          T.hint,
          "rounded-md border border-warning/30 bg-warning/5 px-3 py-2",
        )}
      >
        Each tool calls one plane, and the plane checks the token&apos;s
        audience. A token from signing in is issued for a single plane, so it
        reaches only that plane&apos;s tools; an API token can cover several.
      </p>
    </Card>
  );
}
