"use client";

import { useMemo, useState } from "react";
import { MagnifyingGlassIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/input";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type {
  MCPInspectResponse,
  MCPTool,
} from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { T } from "@/lib/ui/typography";

import { MCP_PLANES, planeTone } from "./mcp-status";

const ALL = "__all__";

function EffectBadge({ mutates }: { mutates: boolean }) {
  return mutates ? (
    <Badge variant="warning" className={T.labelTight}>
      changes state
    </Badge>
  ) : (
    <Badge variant="outline" className={T.labelTight}>
      read
    </Badge>
  );
}

/**
 * Every tool the server registers, and which profile exposes it. Filters
 * answer the two questions this list is opened for: what can an agent on
 * profile X do, and what touches the data plane.
 */
export function MCPTools({ inspect }: { inspect: MCPInspectResponse }) {
  const [query, setQuery] = useState("");
  const [plane, setPlane] = useState<string>(ALL);
  const [profile, setProfile] = useState<string>(
    inspect.transports?.http?.profile || ALL,
  );
  const inProfile = useMemo(() => {
    const p = inspect.profiles.find((x) => x.name === profile);
    return p ? new Set(p.tools) : null;
  }, [inspect.profiles, profile]);
  const denied = useMemo(
    () => new Set(inspect.alwaysDeny),
    [inspect.alwaysDeny],
  );

  const rows = inspect.toolCatalog.filter((t: MCPTool) => {
    const q = query.trim().toLowerCase();
    if (q && !`${t.name} ${t.description}`.toLowerCase().includes(q))
      return false;
    if (plane !== ALL && t.audience !== plane) return false;
    if (inProfile && !inProfile.has(t.name)) return false;
    return true;
  });

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-55 flex-1">
          <MagnifyingGlassIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            aria-label="Search tools"
            placeholder="Search by name or description…"
            className="pl-9"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <SelectRoot value={plane} onValueChange={setPlane}>
          <SelectTrigger aria-label="Plane" className="w-[150px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All planes</SelectItem>
            {MCP_PLANES.map((p) => (
              <SelectItem key={p} value={p}>
                {p} plane
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
        <SelectRoot value={profile} onValueChange={setProfile}>
          <SelectTrigger aria-label="Profile" className="w-50">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Every registered tool</SelectItem>
            {inspect.profiles.map((p) => (
              <SelectItem key={p.name} value={p.name}>
                Profile {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
        <span className={T.hint}>
          {rows.length} of {inspect.toolCatalog.length}
        </span>
      </div>
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Tool</TableHead>
              <TableHead>Plane</TableHead>
              <TableHead>Effect</TableHead>
              <TableHead className="hidden @md:table-cell">
                Capability op
              </TableHead>
              <TableHead className="hidden @2xl:table-cell">
                Description
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="py-8 text-center text-xs text-muted-foreground"
                >
                  No tool matches.
                </TableCell>
              </TableRow>
            ) : (
              rows.map((t) => (
                <TableRow key={t.name}>
                  <TableCell className={T.code}>
                    {t.name}
                    {denied.has(t.name) ? (
                      <Badge
                        variant="destructive"
                        className={`ml-2 ${T.labelTight}`}
                      >
                        always denied
                      </Badge>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={planeTone(t.audience)}
                      className={T.labelTight}
                    >
                      {t.audience}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <EffectBadge mutates={t.mutates} />
                  </TableCell>
                  <TableCell
                    className={`hidden @md:table-cell ${T.code} text-muted-foreground`}
                  >
                    {t.capabilityOp || "—"}
                  </TableCell>
                  <TableCell className="hidden @2xl:table-cell text-xs text-muted-foreground">
                    {t.description}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}

/** Each profile, where it comes from, and how many tools it leaves visible. */
export function MCPProfiles({ inspect }: { inspect: MCPInspectResponse }) {
  const inUse = new Set(
    [
      inspect.transports?.http?.profile,
      inspect.transports?.stdio?.profile,
    ].filter(Boolean),
  );
  return (
    <Card className="p-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Profile</TableHead>
            <TableHead>Source</TableHead>
            <TableHead className="hidden @md:table-cell">Allows</TableHead>
            <TableHead className="hidden @2xl:table-cell">
              Also denies
            </TableHead>
            <TableHead className="text-right">Tools visible</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {inspect.profiles.map((p) => (
            <TableRow key={p.name}>
              <TableCell>
                <span className={T.code}>{p.name}</span>
                {inUse.has(p.name) ? (
                  <Badge variant="success" className={`ml-2 ${T.labelTight}`}>
                    in use
                  </Badge>
                ) : null}
              </TableCell>
              <TableCell>
                <Badge variant="outline" className={T.labelTight}>
                  {p.source.replace("_", " ")}
                </Badge>
              </TableCell>
              <TableCell className="hidden @md:table-cell">
                <div className="flex flex-wrap gap-1">
                  {p.rawPatterns.map((pat) => (
                    <Badge key={pat} variant="outline" className={T.code}>
                      {pat}
                    </Badge>
                  ))}
                </div>
              </TableCell>
              <TableCell className="hidden @2xl:table-cell">
                <div className="flex flex-wrap gap-1">
                  {p.deny.length === 0 ? (
                    <span className="text-xs text-muted-foreground">—</span>
                  ) : (
                    p.deny.map((d) => (
                      <Badge key={d} variant="destructive" className={T.code}>
                        {d}
                      </Badge>
                    ))
                  )}
                </div>
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {p.tools.length}
                <span className="text-muted-foreground">
                  {" "}
                  / {inspect.toolCatalog.length}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className={`border-t px-4 py-3 ${T.hint}`}>
        Tools visible is after the always-deny list. Pick a profile on the Tools
        tab to see exactly which.
      </p>
    </Card>
  );
}

/** The global deny list, joined to the catalog. */
export function MCPAlwaysDeny({ inspect }: { inspect: MCPInspectResponse }) {
  const byName = new Map(inspect.toolCatalog.map((t) => [t.name, t]));
  if (inspect.alwaysDeny.length === 0) {
    return (
      <Card className="p-8 text-center">
        <p className="text-sm text-warning">
          The always-deny list is empty: every profile can expose credential,
          user and policy tools. The built-in list applies only while{" "}
          <span className={T.code}>mcp.always_deny</span> is left unset.
        </p>
      </Card>
    );
  }
  return (
    <Card className="p-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Denied</TableHead>
            <TableHead>Plane</TableHead>
            <TableHead className="hidden @2xl:table-cell">
              Why it matters
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {inspect.alwaysDeny.map((d) => {
            const t = byName.get(d);
            return (
              <TableRow key={d}>
                <TableCell className={T.code}>{d}</TableCell>
                <TableCell>
                  {t ? (
                    <Badge
                      variant={planeTone(t.audience)}
                      className={T.labelTight}
                    >
                      {t.audience}
                    </Badge>
                  ) : (
                    <Badge variant="outline" className={T.labelTight}>
                      {d.includes("*") ? "pattern" : "not registered"}
                    </Badge>
                  )}
                </TableCell>
                <TableCell className="hidden @2xl:table-cell text-xs text-muted-foreground">
                  {t?.description ??
                    (d.includes("*")
                      ? "Every tool whose name starts with this prefix."
                      : "No tool by this name is registered.")}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      <p className={`border-t px-4 py-3 ${T.hint}`}>
        Applied after every profile, admin included: an agent never sees these
        tools.
      </p>
    </Card>
  );
}
