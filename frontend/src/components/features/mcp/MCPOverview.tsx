"use client";

import { IconTone } from "@/components/ui/IconChip";
import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  XCircleIcon,
  GlobeAltIcon,
  CommandLineIcon,
} from "@heroicons/react/24/outline";

import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { Card, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { SECTION } from "@/lib/navigation/sections";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { planeTone, useMcpLive } from "./mcp-status";
import { errorMessage } from "@/hooks/errorContract";

/** A tile of the MCP pages: a label, its value, and a line on what it means. */
export function Tile({
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
          <>
            {httpProfile ? (
              <>
                Profile <span className={T.code}>{httpProfile}</span> ·{" "}
              </>
            ) : null}
            <Link
              href={SECTION.mcpTools}
              className="text-primary hover:underline"
            >
              see the tools
            </Link>
          </>
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
  const endpoint =
    inspect.oauth?.resourceUrl || `${http?.addr || DEFAULT_HTTP_ADDR}/mcp`;
  return (
    <div className="space-y-4">
      <Card className="p-4">
        <p className={T.helper}>
          {inspect.oauth?.edgeAudience ? (
            <>
              Agents authorize for{" "}
              <span className={T.code}>
                {inspect.oauth.resourceUrl || "the MCP server"}
              </span>{" "}
              and hold a{" "}
              <span className={T.code}>{inspect.oauth.edgeAudience}</span>{" "}
              token, which no plane accepts. The server exchanges it for each
              plane a tool calls, so one session reaches all of them.
            </>
          ) : (
            <>
              Each tool calls one plane, and the plane checks the token&apos;s
              audience. A token from signing in is issued for a single plane, so
              it reaches only that plane&apos;s tools; an API token can cover
              several.
            </>
          )}
        </p>
      </Card>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <TransportCard
          name="Streamable HTTP"
          icon={GlobeAltIcon}
          enabled={!!http?.enabled}
        >
          <ConnectField
            label="Endpoint"
            value={`POST ${endpoint}`}
            copy={endpoint}
          />
          <ConnectField
            label="Token"
            value={
              inspect.oauth?.edgeAudience
                ? `Authorization: Bearer <${inspect.oauth.edgeAudience} token>`
                : "Authorization: Bearer <token>"
            }
          />
          <ConnectField label="Profile" value={http?.profile || "—"} />
          <p className={T.hint}>
            Sessions end after{" "}
            {http?.sessionTimeoutSeconds
              ? `${http.sessionTimeoutSeconds}s`
              : "no set time"}{" "}
            idle and belong to the principal that opened them.
          </p>
        </TransportCard>
        <TransportCard
          name="stdio"
          icon={CommandLineIcon}
          enabled={!!stdio?.enabled}
        >
          <ConnectField
            label="Command"
            value={STDIO_COMMAND}
            copy={STDIO_COMMAND}
          />
          <ConnectField label="Token" value={`${STDIO_TOKEN_ENV}=<token>`} />
          <ConnectField label="Profile" value={stdio?.profile || "—"} />
          <p className={T.hint}>
            One process serves one operator; its token is used as it is, never
            exchanged.
          </p>
        </TransportCard>
      </div>
    </div>
  );
}

// The HTTP listener's address when the configuration names none.
const DEFAULT_HTTP_ADDR = ":8095";
const STDIO_COMMAND = "paladin serve mcp --transport stdio";
const STDIO_TOKEN_ENV = "PALADIN_MCP_TOKEN";

function TransportCard({
  name,
  icon,
  enabled,
  children,
}: {
  name: string;
  icon: React.ElementType;
  enabled: boolean;
  children: React.ReactNode;
}) {
  return (
    <Card className="space-y-3 p-4">
      <div className="flex items-center gap-2">
        <CardTitle
          level={3}
          className={T.cardTitleProse}
          icon={icon}
          tone={enabled ? IconTone.Success : IconTone.Neutral}
        >
          {name}
        </CardTitle>
        <Badge variant={enabled ? "success" : "outline"}>
          {enabled ? "enabled" : "disabled"}
        </Badge>
      </div>
      {children}
    </Card>
  );
}

/** A connection setting: its label, the value in mono, and a copy button. */
function ConnectField({
  label,
  value,
  copy,
}: {
  label: string;
  value: string;
  copy?: string;
}) {
  return (
    <div className="space-y-1">
      <div className={T.label}>{label}</div>
      <div className="flex items-start gap-2">
        <code className={cn(T.code, "min-w-0 flex-1 break-all")}>{value}</code>
        {copy ? (
          <IdentifierCopy value={copy} label={label} iconOnly className="p-1" />
        ) : null}
      </div>
    </div>
  );
}
