"use client";

import React, { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
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
import { MCPLiveSessions } from "@/components/features/mcp/MCPLiveSessions";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

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
  const inspectQuery = useQuery({
    queryKey: ["mcpInspect"],
    queryFn: ({ signal }) => mcpInspectClient.inspect({}, { signal }),
  });
  const data = inspectQuery.data ?? null;
  const loading = inspectQuery.isFetching;
  const error = inspectQuery.error
    ? inspectQuery.error instanceof ConnectError
      ? inspectQuery.error.rawMessage
      : "Inspect failed"
    : null;
  const refresh = () => void inspectQuery.refetch();

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
            onClick={refresh}
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
              <Badge variant="secondary" className={cn("ml-1.5", T.labelTight)}>
                {profiles.length}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="deny">
              <NoSymbolIcon className="size-4" /> Always-Deny
              <Badge variant="secondary" className={cn("ml-1.5", T.labelTight)}>
                {denyList.length}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="tools">
              <CommandLineIcon className="size-4" /> Tools
              <Badge variant="secondary" className={cn("ml-1.5", T.labelTight)}>
                {tools.length}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="upstreams">
              <ServerStackIcon className="size-4" /> Upstreams &amp; Transports
            </TabsTrigger>
          </TabsList>

          {/* ── Profiles ──────────────────────────────────────────── */}
          {/* Table layout mirrors the Tools tab so operators get a
              consistent at-a-glance view: name + source badge + counts
              + the same Expanded-tools disclosure as before, but the
              row pattern is uniform across tabs. The expansion still
              renders the post-deny tool list so an operator can see
              exactly what an agent on this profile sees. */}
          <TabsContent value="profiles" className="space-y-3">
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
            <Card className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Source</TableHead>
                    <TableHead className="hidden md:table-cell">
                      Raw patterns
                    </TableHead>
                    <TableHead className="hidden lg:table-cell">
                      Profile deny
                    </TableHead>
                    <TableHead className="w-[140px]">Tools visible</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {profiles.map((p) => (
                    <React.Fragment key={p.name}>
                      <TableRow>
                        <TableCell className={T.code}>{p.name}</TableCell>
                        <TableCell>
                          <Badge
                            variant={sourceTone(p.source)}
                            className={T.labelTight}
                          >
                            {p.source.replace("_", " ")}
                          </Badge>
                        </TableCell>
                        <TableCell className="hidden md:table-cell">
                          <div className="flex flex-wrap gap-1">
                            {p.rawPatterns.length === 0 ? (
                              <span className="text-xs text-muted-foreground">
                                —
                              </span>
                            ) : (
                              p.rawPatterns.map((pat) => (
                                <Badge
                                  key={pat}
                                  variant="outline"
                                  className={T.code}
                                >
                                  {pat}
                                </Badge>
                              ))
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="hidden lg:table-cell">
                          <div className="flex flex-wrap gap-1">
                            {p.deny.length === 0 ? (
                              <span className="text-xs text-muted-foreground">
                                —
                              </span>
                            ) : (
                              p.deny.map((d) => (
                                <Badge
                                  key={d}
                                  variant="destructive"
                                  className={T.code}
                                >
                                  {d}
                                </Badge>
                              ))
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="text-xs">
                          <details className="group">
                            <summary className="cursor-pointer hover:text-foreground">
                              <span className="font-mono tabular-nums">
                                {p.tools.length}
                              </span>{" "}
                              of {tools.length}
                            </summary>
                            <div className="mt-2 flex flex-wrap gap-1">
                              {p.tools.map((tname) => {
                                const t = toolIndex.get(tname);
                                return (
                                  <Badge
                                    key={tname}
                                    variant={
                                      t ? audienceTone(t.audience) : "outline"
                                    }
                                    className={T.code}
                                    title={t?.description ?? ""}
                                  >
                                    {tname}
                                  </Badge>
                                );
                              })}
                            </div>
                          </details>
                        </TableCell>
                      </TableRow>
                    </React.Fragment>
                  ))}
                  {profiles.length === 0 && (
                    <TableRow>
                      <TableCell
                        colSpan={5}
                        className="py-8 text-center text-xs text-muted-foreground"
                      >
                        No profiles configured.
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>

          {/* ── Always-Deny ───────────────────────────────────────── */}
          {/* Same table shape as Tools — one row per denied tool name,
              joined back to the tool catalog so operators see what
              they're blocking (audience, mutates flag, description)
              without bouncing between tabs. Wildcard patterns render
              with a "pattern" pseudo-audience and no catalog row. */}
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
            <Card className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Pattern</TableHead>
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
                  {denyList.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={5}
                        className="py-8 text-center text-xs text-muted-foreground italic"
                      >
                        Empty list — no global denies. Operator opted out of the
                        built-in safe defaults.
                      </TableCell>
                    </TableRow>
                  ) : (
                    denyList.map((d) => {
                      const t = toolIndex.get(d);
                      const isWildcard = d.includes("*");
                      return (
                        <TableRow key={d}>
                          <TableCell className={T.code}>{d}</TableCell>
                          <TableCell>
                            {t ? (
                              <Badge
                                variant={audienceTone(t.audience)}
                                className={T.labelTight}
                              >
                                {t.audience}
                              </Badge>
                            ) : isWildcard ? (
                              <Badge variant="outline" className={T.labelTight}>
                                pattern
                              </Badge>
                            ) : (
                              <span className="text-xs text-muted-foreground">
                                —
                              </span>
                            )}
                          </TableCell>
                          <TableCell
                            className={cn(
                              "hidden md:table-cell",
                              T.code,
                              "text-muted-foreground",
                            )}
                          >
                            {t?.capabilityOp || "—"}
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant="destructive"
                              className={T.labelTight}
                            >
                              denied
                            </Badge>
                          </TableCell>
                          <TableCell className="hidden lg:table-cell text-xs text-muted-foreground">
                            {t?.description ||
                              (isWildcard
                                ? "wildcard pattern — matches every tool whose name has this prefix"
                                : "tool not in current catalog (chart skew)")}
                          </TableCell>
                        </TableRow>
                      );
                    })
                  )}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>

          {/* ── Tools ─────────────────────────────────────────────── */}
          <TabsContent value="tools" className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Every tool the MCP bridge registers, with its target plane and
              (when applicable) the capability op the caller&apos;s capability
              must include. Tools marked{" "}
              <Badge variant="warning" className={T.labelTight}>
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
                      <TableCell className={T.code}>{t.name}</TableCell>
                      <TableCell>
                        <Badge
                          variant={audienceTone(t.audience)}
                          className={T.labelTight}
                        >
                          {t.audience}
                        </Badge>
                      </TableCell>
                      <TableCell
                        className={cn(
                          "hidden md:table-cell",
                          T.code,
                          "text-muted-foreground",
                        )}
                      >
                        {t.capabilityOp || "—"}
                      </TableCell>
                      <TableCell>
                        {t.mutates ? (
                          <Badge variant="warning" className={T.labelTight}>
                            mutates
                          </Badge>
                        ) : (
                          <Badge variant="outline" className={T.labelTight}>
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
                      className={T.labelTight}
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
                      className={T.labelTight}
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

            <MCPLiveSessions />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}
