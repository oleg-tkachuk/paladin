"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  ArrowPathIcon,
  CommandLineIcon,
  ExclamationTriangleIcon,
  NoSymbolIcon,
  ShieldCheckIcon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";
import { ConnectError } from "@connectrpc/connect";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { mcpInspectClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import type { MCPInspectResponse } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";

// /mcp — operator visibility for the MCP (Model Context Protocol)
// bridge. Single page with tabs because all four views share one
// Inspect() call: profiles, deny-list, tool catalog, and upstream
// dispatch URLs. Loaded once, refreshed on demand.
//
// Authorisation: platform-admin via Cedar (server-side gate). UI
// shows the ConnectError directly when denied so non-admins see
// "permission denied" rather than an empty page.

function audienceTone(
  aud: string,
): "destructive" | "warning" | "info" | "success" | "outline" {
  switch (aud) {
    case "admin":
      return "warning";
    case "data":
      return "info";
    case "iam":
      return "destructive";
    default:
      return "outline";
  }
}

function sourceTone(
  src: string,
): "destructive" | "warning" | "info" | "success" | "outline" {
  switch (src) {
    case "user_override":
      return "warning";
    case "user_only":
      return "info";
    default:
      return "outline";
  }
}

export default function MCPInspectPage() {
  const [data, setData] = useState<MCPInspectResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetch = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await mcpInspectClient.inspect({});
      setData(res);
    } catch (err) {
      setError(err instanceof ConnectError ? err.rawMessage : "Inspect failed");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetch();
  }, []);

  const profiles = useMemo(() => data?.profiles ?? [], [data]);
  const tools = useMemo(() => data?.toolCatalog ?? [], [data]);
  const denyList = data?.alwaysDeny ?? [];
  const upstreams = data?.upstreams;
  const transports = data?.transports;

  // Index tool metadata by name so the profiles tab can render the
  // expanded allow-list with audience colour-coding without a second
  // pass through the catalog.
  const toolIndex = useMemo(() => {
    const m = new Map<string, (typeof tools)[number]>();
    for (const t of tools) m.set(t.name, t);
    return m;
  }, [tools]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="MCP Bridge"
        description="Operator-side view of the MCP (Model Context Protocol) bridge: tool catalog, profile allow-lists, deny-list, and upstream dispatch."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void fetch()}
            disabled={loading}
          >
            <ArrowPathIcon
              className={cn("size-4", loading && "animate-spin")}
            />
            Refresh
          </Button>
        }
      />

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          <ExclamationTriangleIcon className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {loading && !data ? (
        <Card className="p-6">
          <Skeleton className="h-8 w-64" />
          <div className="mt-4 space-y-2">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-5/6" />
            <Skeleton className="h-4 w-4/6" />
          </div>
        </Card>
      ) : (
        <Tabs defaultValue="profiles">
          <TabsList>
            <TabsTrigger value="profiles">
              <ShieldCheckIcon className="size-4" /> Profiles
              <Badge variant="secondary" className="ml-1.5 text-[10px]">
                {profiles.length}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="deny">
              <NoSymbolIcon className="size-4" /> Always-Deny
              <Badge variant="secondary" className="ml-1.5 text-[10px]">
                {denyList.length}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="tools">
              <CommandLineIcon className="size-4" /> Tools
              <Badge variant="secondary" className="ml-1.5 text-[10px]">
                {tools.length}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="upstreams">
              <ServerStackIcon className="size-4" /> Upstreams &amp; Transports
            </TabsTrigger>
          </TabsList>

          {/* ── Profiles ──────────────────────────────────────────── */}
          <TabsContent value="profiles" className="space-y-4">
            <p className="text-sm text-muted-foreground">
              Each transport (stdio / streamable-HTTP) selects a profile by
              name. <code className="text-xs">read_only</code>,{" "}
              <code className="text-xs">agent_safe</code>, and{" "}
              <code className="text-xs">admin</code> are built-ins; the
              <code className="text-xs"> user_override</code> badge marks
              profiles whose tool list was changed by{" "}
              <code className="text-xs">cfg.MCP.Profiles</code> overrides.
              Patterns support a trailing <code className="text-xs">*</code>{" "}
              wildcard. The expanded list shows what agents actually see after
              the always-deny denylist is applied.
            </p>
            {profiles.map((p) => (
              <Card key={p.name} className="p-4 space-y-3">
                <div className="flex items-center gap-2 flex-wrap">
                  <h3 className="text-base font-semibold font-mono">
                    {p.name}
                  </h3>
                  <Badge variant={sourceTone(p.source)} className="text-[10px]">
                    {p.source.replace("_", " ")}
                  </Badge>
                  <span className="text-xs text-muted-foreground ml-auto">
                    {p.tools.length} of {tools.length} tools visible
                  </span>
                </div>

                <div>
                  <div className="text-xs font-medium text-muted-foreground mb-1">
                    Raw patterns
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {p.rawPatterns.map((pat) => (
                      <Badge
                        key={pat}
                        variant="outline"
                        className="font-mono text-[11px]"
                      >
                        {pat}
                      </Badge>
                    ))}
                  </div>
                </div>

                {p.deny.length > 0 && (
                  <div>
                    <div className="text-xs font-medium text-muted-foreground mb-1">
                      Profile deny (overrides allow)
                    </div>
                    <div className="flex flex-wrap gap-1">
                      {p.deny.map((d) => (
                        <Badge
                          key={d}
                          variant="destructive"
                          className="font-mono text-[11px]"
                        >
                          {d}
                        </Badge>
                      ))}
                    </div>
                  </div>
                )}

                <details className="group">
                  <summary className="text-xs font-medium text-muted-foreground cursor-pointer hover:text-foreground">
                    Expanded tool list ({p.tools.length})
                  </summary>
                  <div className="mt-2 flex flex-wrap gap-1">
                    {p.tools.map((tname) => {
                      const t = toolIndex.get(tname);
                      return (
                        <Badge
                          key={tname}
                          variant={t ? audienceTone(t.audience) : "outline"}
                          className="font-mono text-[11px]"
                          title={t?.description ?? ""}
                        >
                          {tname}
                        </Badge>
                      );
                    })}
                  </div>
                </details>
              </Card>
            ))}
          </TabsContent>

          {/* ── Always-Deny ───────────────────────────────────────── */}
          <TabsContent value="deny" className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Global blacklist applied <em>after</em> profile expansion
              regardless of which profile a transport selects. Encodes the
              &ldquo;antithesis to capability model&rdquo; set: tool names an
              agentic runtime must never see in its catalog (issuing/ revoking
              its own capability, minting M2M tokens, managing users, rewriting
              policies). Wins over every profile, including{" "}
              <code className="text-xs">admin</code>.
            </p>
            <Card className="p-4">
              <div className="flex flex-wrap gap-1.5">
                {denyList.length === 0 ? (
                  <span className="text-sm text-muted-foreground italic">
                    Empty list — no global denies. Operator opted out of the
                    built-in safe defaults.
                  </span>
                ) : (
                  denyList.map((d) => (
                    <Badge
                      key={d}
                      variant="destructive"
                      className="font-mono text-[11px]"
                    >
                      {d}
                    </Badge>
                  ))
                )}
              </div>
            </Card>
          </TabsContent>

          {/* ── Tools ─────────────────────────────────────────────── */}
          <TabsContent value="tools" className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Every tool the MCP bridge registers, with its target plane and
              (when applicable) the capability op the caller&apos;s capability
              must include. Tools marked{" "}
              <Badge variant="warning" className="text-[10px]">
                mutates
              </Badge>{" "}
              issue state-changing RPCs and are deny-list candidates by default.
            </p>
            <Card className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Audience</TableHead>
                    <TableHead className="hidden md:table-cell">
                      Capability op
                    </TableHead>
                    <TableHead>Effect</TableHead>
                    <TableHead className="hidden lg:table-cell">
                      Description
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {tools.map((t) => (
                    <TableRow key={t.name}>
                      <TableCell className="font-mono text-[11px]">
                        {t.name}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={audienceTone(t.audience)}
                          className="text-[10px]"
                        >
                          {t.audience}
                        </Badge>
                      </TableCell>
                      <TableCell className="hidden md:table-cell font-mono text-[11px] text-muted-foreground">
                        {t.capabilityOp || "—"}
                      </TableCell>
                      <TableCell>
                        {t.mutates ? (
                          <Badge variant="warning" className="text-[10px]">
                            mutates
                          </Badge>
                        ) : (
                          <Badge variant="outline" className="text-[10px]">
                            read
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell className="hidden lg:table-cell text-xs text-muted-foreground">
                        {t.description}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>

          {/* ── Upstreams + Transports ────────────────────────────── */}
          <TabsContent value="upstreams" className="space-y-4">
            <Card className="p-4 space-y-3">
              <h3 className="text-sm font-semibold">Upstreams</h3>
              <p className="text-xs text-muted-foreground">
                URLs the MCP bridge dispatches to. Not secrets — the per-
                request bearer token is what gates access. Empty values mean the
                configured fallback (in-cluster Service DNS) is used at request
                time.
              </p>
              <dl className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                <div>
                  <dt className="text-xs text-muted-foreground">data</dt>
                  <dd className="font-mono text-[12px]">
                    {upstreams?.dataUrl || (
                      <span className="text-muted-foreground italic">
                        (default)
                      </span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-muted-foreground">admin</dt>
                  <dd className="font-mono text-[12px]">
                    {upstreams?.adminUrl || (
                      <span className="text-muted-foreground italic">
                        (default)
                      </span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-muted-foreground">iam</dt>
                  <dd className="font-mono text-[12px]">
                    {upstreams?.iamUrl || (
                      <span className="text-muted-foreground italic">
                        (default)
                      </span>
                    )}
                  </dd>
                </div>
              </dl>
            </Card>

            <Card className="p-4 space-y-3">
              <h3 className="text-sm font-semibold">Transports</h3>
              <p className="text-xs text-muted-foreground">
                The MCP server runs in two modes: stdio for local agents (Claude
                Desktop, Cursor) and streamable-HTTP for remote agentic
                platforms. Each transport pins its own profile.
              </p>
              <dl className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="rounded-md border p-3">
                  <div className="flex items-center justify-between">
                    <dt className="text-sm font-medium">stdio</dt>
                    <Badge
                      variant={
                        transports?.stdio?.enabled ? "success" : "outline"
                      }
                      className="text-[10px]"
                    >
                      {transports?.stdio?.enabled ? "enabled" : "disabled"}
                    </Badge>
                  </div>
                  <dd className="mt-1.5 text-xs text-muted-foreground">
                    Profile:{" "}
                    <code className="font-mono">
                      {transports?.stdio?.profile || "(default)"}
                    </code>
                  </dd>
                </div>
                <div className="rounded-md border p-3">
                  <div className="flex items-center justify-between">
                    <dt className="text-sm font-medium">streamable-HTTP</dt>
                    <Badge
                      variant={
                        transports?.http?.enabled ? "success" : "outline"
                      }
                      className="text-[10px]"
                    >
                      {transports?.http?.enabled ? "enabled" : "disabled"}
                    </Badge>
                  </div>
                  <dd className="mt-1.5 space-y-0.5 text-xs text-muted-foreground">
                    <div>
                      Profile:{" "}
                      <code className="font-mono">
                        {transports?.http?.profile || "(default)"}
                      </code>
                    </div>
                    <div>
                      Addr:{" "}
                      <code className="font-mono">
                        {transports?.http?.addr || "(default)"}
                      </code>
                    </div>
                    <div>
                      Session timeout:{" "}
                      <code className="font-mono">
                        {transports?.http?.sessionTimeoutSeconds
                          ? `${transports.http.sessionTimeoutSeconds}s`
                          : "0 (no timeout)"}
                      </code>
                    </div>
                  </dd>
                </div>
              </dl>
            </Card>

            {/*
             * Sessions live-state — deferred. The streamable-HTTP transport
             * keeps session state inside github.com/modelcontextprotocol/
             * go-sdk/mcp.Server which doesn't expose an enumeration hook.
             * Building this view requires either forking the SDK or
             * wrapping its Server with a ServeHTTP-level middleware that
             * tracks session_id from the X-Session-Id header. Tracked in
             * BACKLOG.md under "MCP live sessions".
             */}
            <Card className="p-4 border-dashed">
              <div className="flex items-center gap-2">
                <ServerStackIcon className="size-4 text-muted-foreground" />
                <h3 className="text-sm font-medium">Live sessions</h3>
                <Badge variant="outline" className="text-[10px]">
                  deferred
                </Badge>
              </div>
              <p className="mt-2 text-xs text-muted-foreground">
                The MCP SDK currently doesn&apos;t expose a session enumeration
                hook on its streamable-HTTP server. Building a live session
                table needs either a fork of{" "}
                <code className="text-[11px]">modelcontextprotocol/go-sdk</code>{" "}
                or a wrapping middleware that tracks session-id headers. Tracked
                in BACKLOG. In the meantime, MCP tool calls are visible in the
                audit log — filter by{" "}
                <Link
                  href="/audit?audience=paladin-mcp"
                  className="text-primary hover:underline"
                >
                  audience=paladin-mcp
                </Link>
                .
              </p>
            </Card>
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}
