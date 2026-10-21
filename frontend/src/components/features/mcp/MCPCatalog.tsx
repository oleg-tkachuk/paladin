"use client";

import { useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
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
import { useTableView } from "@/hooks/useTableView";
import type { TableUrl } from "@/lib/table/urlState";
import type { SortAccessors } from "@/lib/table/view";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { MCP_PLANES, planeTone } from "./mcp-status";

// The catalog keeps its search and sort in the URL under its own names, beside the
// page's other tables.
const TOOLS_TABLE: TableUrl = { namespace: "tools" };

const ALL = "__all__";

// The columns of the tools table; the empty row spans them.
const TOOL_COLUMNS = 5;

// How much a call can change, for sorting by effect: reads first.
const EFFECT_RANK = { read: 0, mutates: 1, destructive: 2 } as const;

type ToolColumn = "tool" | "plane" | "effect" | "op";
const TOOL_SORT: SortAccessors<MCPTool, ToolColumn> = {
  tool: (t) => t.name,
  plane: (t) => t.audience,
  effect: (t) =>
    t.destructive
      ? EFFECT_RANK.destructive
      : t.mutates
        ? EFFECT_RANK.mutates
        : EFFECT_RANK.read,
  op: (t) => t.capabilityOp || undefined,
};
const TOOL_SEARCH = (t: MCPTool) => [t.name, t.description, t.capabilityOp];

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

  const narrowed = inspect.toolCatalog.filter(
    (t: MCPTool) =>
      (plane === ALL || t.audience === plane) &&
      (!inProfile || inProfile.has(t.name)),
  );
  const view = useTableView(narrowed, {
    text: TOOL_SEARCH,
    accessors: TOOL_SORT,
    url: TOOLS_TABLE,
  });
  const rows = view.rows;
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  return (
    <div className="space-y-3">
      <TableToolbar
        query={view.query}
        onQueryChange={view.setQuery}
        searchLabel="Search tools"
        placeholder="Search by name or description…"
        count={{ shown: rows.length, total: inspect.toolCatalog.length }}
      >
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
      </TableToolbar>
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead label="Tool" column="tool" {...sortProps} />
              <SortableHead label="Plane" column="plane" {...sortProps} />
              <SortableHead label="Effect" column="effect" {...sortProps} />
              <SortableHead
                label="Capability op"
                column="op"
                className="hidden @md:table-cell"
                {...sortProps}
              />
              <StaticHead className="hidden @2xl:table-cell">
                Description
              </StaticHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={TOOL_COLUMNS}
                  className="py-8 text-center text-xs text-muted-foreground"
                >
                  No tool matches.
                </TableCell>
              </TableRow>
            ) : (
              rows.map((t) => (
                <TableRow key={t.name} {...openableRow(() => setOpened(t))}>
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
            <StaticHead>Profile</StaticHead>
            <StaticHead>Source</StaticHead>
            <StaticHead className="hidden @md:table-cell">Allows</StaticHead>
            <StaticHead className="text-right">Tools visible</StaticHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {inspect.profiles.map((p) => (
            <TableRow key={p.name} {...openableRow(() => setOpened(p))}>
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
            <StaticHead>Denied</StaticHead>
            <StaticHead>Plane</StaticHead>
            <StaticHead className="hidden @2xl:table-cell">
              Why it matters
            </StaticHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {inspect.alwaysDeny.map((d) => {
            const t = byName.get(d);
            return (
              <TableRow key={d} {...(t ? openableRow(() => setOpened(t)) : {})}>
                {/* A long name wraps, so the plane chip beside it keeps
                    its width on a phone instead of running off the card. */}
                <TableCell
                  className={cn(T.code, "whitespace-normal break-all")}
                >
                  {d}
                </TableCell>
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
