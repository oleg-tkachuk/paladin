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
  MCPProfile,
  MCPTool,
} from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import {
  RecordDialog,
  RecordField,
  openableRow,
} from "@/components/ui/RecordDialog";
import { T } from "@/lib/ui/typography";

import { MCP_PLANES, planeTone } from "./mcp-status";

const ALL = "__all__";

/** What a call does: reads, changes state, or may destroy something — and
 * whether it reaches past Paladin. The server sends the same as MCP hints. */
export function EffectBadge({
  tool,
}: {
  tool: Pick<MCPTool, "mutates" | "destructive" | "openWorld">;
}) {
  return (
    <span className="inline-flex flex-wrap gap-1">
      {tool.destructive ? (
        <Badge variant="destructive">destructive</Badge>
      ) : tool.mutates ? (
        <Badge variant="warning">changes state</Badge>
      ) : (
        <Badge variant="outline">read</Badge>
      )}
      {tool.openWorld ? <Badge variant="info">open world</Badge> : null}
    </span>
  );
}

// How many of a profile's patterns its row shows before "+N more".
const PATTERNS_SHOWN = 4;

/** Each tool's record: everything the row shows and what it cuts. */
export function ToolDialog({
  tool,
  inspect,
  onClose,
}: {
  tool: MCPTool | null;
  inspect: MCPInspectResponse;
  onClose: () => void;
}) {
  const profiles = tool
    ? inspect.profiles.filter((p) => p.tools.includes(tool.name))
    : [];
  const denied = tool ? inspect.alwaysDeny.includes(tool.name) : false;
  return (
    <RecordDialog
      open={tool !== null}
      onOpenChange={(o) => !o && onClose()}
      title="Tool"
      subtitle={tool?.name}
    >
      {tool ? (
        <>
          <RecordField label="Plane">
            <Badge variant={planeTone(tool.audience)}>{tool.audience}</Badge>
          </RecordField>
          <RecordField label="Effect">
            <EffectBadge tool={tool} />
          </RecordField>
          <RecordField label="Description">
            {tool.description || "—"}
          </RecordField>
          <RecordField label="Capability op" mono>
            {tool.capabilityOp || "— (a JWT is enough)"}
          </RecordField>
          <RecordField label="Offered by">
            {denied ? (
              <Badge variant="destructive">always denied</Badge>
            ) : profiles.length === 0 ? (
              <span className="text-muted-foreground">no profile</span>
            ) : (
              <span className="inline-flex flex-wrap gap-1">
                {profiles.map((p) => (
                  <Badge key={p.name} variant="outline" className={T.code}>
                    {p.name}
                  </Badge>
                ))}
              </span>
            )}
          </RecordField>
        </>
      ) : null}
    </RecordDialog>
  );
}

/** A profile's record: its patterns and denials whole, and what they leave. */
function ProfileDialog({
  profile,
  inspect,
  onClose,
}: {
  profile: MCPProfile | null;
  inspect: MCPInspectResponse;
  onClose: () => void;
}) {
  return (
    <RecordDialog
      open={profile !== null}
      onOpenChange={(o) => !o && onClose()}
      title="Profile"
      subtitle={profile?.name}
    >
      {profile ? (
        <>
          <RecordField label="Source">
            <Badge variant="outline">{profile.source.replace("_", " ")}</Badge>
          </RecordField>
          <RecordField label="Allows">
            <PatternList patterns={profile.rawPatterns} />
          </RecordField>
          <RecordField label="Also denies">
            {profile.deny.length ? (
              <PatternList patterns={profile.deny} variant="destructive" />
            ) : (
              "—"
            )}
          </RecordField>
          <RecordField
            label={`Tools (${profile.tools.length} of ${inspect.toolCatalog.length})`}
          >
            <PatternList patterns={profile.tools} />
          </RecordField>
        </>
      ) : null}
    </RecordDialog>
  );
}

function PatternList({
  patterns,
  variant = "outline",
}: {
  patterns: readonly string[];
  variant?: "outline" | "destructive";
}) {
  return (
    <span className="inline-flex flex-wrap gap-1">
      {patterns.map((p) => (
        <Badge key={p} variant={variant} className={T.code}>
          {p}
        </Badge>
      ))}
    </span>
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
  const [opened, setOpened] = useState<MCPTool | null>(null);

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
                <TableRow
                  key={t.name}
                  {...openableRow(() => setOpened(t), `Open tool ${t.name}`)}
                >
                  <TableCell className={T.code}>
                    {t.name}
                    {denied.has(t.name) ? (
                      <Badge variant="destructive" className="ml-2">
                        always denied
                      </Badge>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <Badge variant={planeTone(t.audience)}>{t.audience}</Badge>
                  </TableCell>
                  <TableCell>
                    <EffectBadge tool={t} />
                  </TableCell>
                  <TableCell
                    className={`hidden @md:table-cell ${T.code} text-muted-foreground`}
                  >
                    {t.capabilityOp || "—"}
                  </TableCell>
                  <TableCell className="hidden @2xl:table-cell whitespace-normal text-muted-foreground">
                    {t.description}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
      <ToolDialog
        tool={opened}
        inspect={inspect}
        onClose={() => setOpened(null)}
      />
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
  const [opened, setOpened] = useState<MCPProfile | null>(null);
  return (
    <Card className="p-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Profile</TableHead>
            <TableHead>Source</TableHead>
            <TableHead className="hidden @md:table-cell">Allows</TableHead>
            <TableHead className="text-right">Tools visible</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {inspect.profiles.map((p) => (
            <TableRow
              key={p.name}
              {...openableRow(() => setOpened(p), `Open profile ${p.name}`)}
            >
              <TableCell>
                <span className={T.code}>{p.name}</span>
                {inUse.has(p.name) ? (
                  <Badge variant="success" className="ml-2">
                    in use
                  </Badge>
                ) : null}
              </TableCell>
              <TableCell>
                <Badge variant="outline">{p.source.replace("_", " ")}</Badge>
              </TableCell>
              <TableCell className="hidden @md:table-cell whitespace-normal">
                <span className="inline-flex flex-wrap items-center gap-1">
                  {p.rawPatterns.slice(0, PATTERNS_SHOWN).map((pat) => (
                    <Badge key={pat} variant="outline" className={T.code}>
                      {pat}
                    </Badge>
                  ))}
                  {p.rawPatterns.length > PATTERNS_SHOWN ? (
                    <span className="text-muted-foreground">
                      +{p.rawPatterns.length - PATTERNS_SHOWN} more
                    </span>
                  ) : null}
                  {p.deny.length ? (
                    <span className="text-muted-foreground">
                      · denies {p.deny.length}
                    </span>
                  ) : null}
                </span>
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
        Tools visible is after the always-deny list. Open a profile to see
        exactly which.
      </p>
      <ProfileDialog
        profile={opened}
        inspect={inspect}
        onClose={() => setOpened(null)}
      />
    </Card>
  );
}

/** The global deny list, joined to the catalog. */
export function MCPAlwaysDeny({ inspect }: { inspect: MCPInspectResponse }) {
  const byName = new Map(inspect.toolCatalog.map((t) => [t.name, t]));
  const [opened, setOpened] = useState<MCPTool | null>(null);
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
              <TableRow
                key={d}
                {...(t
                  ? openableRow(() => setOpened(t), `Open tool ${d}`)
                  : {})}
              >
                <TableCell className={T.code}>{d}</TableCell>
                <TableCell>
                  {t ? (
                    <Badge variant={planeTone(t.audience)}>{t.audience}</Badge>
                  ) : (
                    <Badge variant="outline">
                      {d.includes("*") ? "pattern" : "not registered"}
                    </Badge>
                  )}
                </TableCell>
                <TableCell className="hidden @2xl:table-cell whitespace-normal text-muted-foreground">
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
      <ToolDialog
        tool={opened}
        inspect={inspect}
        onClose={() => setOpened(null)}
      />
    </Card>
  );
}
