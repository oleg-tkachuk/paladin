"use client";

// A topology drawn as Argo CD draws an application's resource tree: columns
// left to right, one box per thing with its kind, name and state, curves for
// what holds or serves what. lib/topology decides every position; this only
// places it. Nodes are HTML over an SVG of edges, so each is a real link: it
// takes focus in the order the columns read, opens on Enter, and carries the
// same StatusIcon as every list. Colours are theme tokens alone, so it reads
// in every palette, and nothing moves: zoom and grouping redraw in place.
// Pointing at a node, or focusing it, lights its parents, its children and
// the curves to them, and lets the rest of the graph recede.

import React, { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  ArchiveBoxIcon,
  ArrowsPointingInIcon,
  BuildingOfficeIcon,
  EllipsisVerticalIcon,
  FolderIcon,
  MagnifyingGlassMinusIcon,
  MagnifyingGlassPlusIcon,
  RectangleGroupIcon,
  ServerStackIcon,
  IdentificationIcon,
  ShieldCheckIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { StatusIcon } from "@/components/ui/StatusIcon";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { groupSiblings, isGroup } from "@/lib/topology/group";
import {
  NODE_HEIGHT,
  NODE_WIDTH,
  layoutGraph,
  type PlacedNode,
} from "@/lib/topology/layout";
import {
  KIND_NOUN,
  edgeKey,
  neighbourhood,
  nodeMatches,
  type NodeKind,
  type TopologyGraph as Graph,
} from "@/lib/topology/model";
import {
  ZOOM_MAX,
  ZOOM_MIN,
  fitZoom,
  zoomBy,
  zoomPercent,
} from "@/lib/topology/zoom";
import { STATUS_ORDER, type StatusTone } from "@/lib/ui/status";
import { FOCUS_RING, HOVER_SURFACE } from "@/lib/ui/states";
import { cn } from "@/lib/utils";

/** Siblings past this many fold into one node while grouping is on. */
const GROUP_ABOVE = 5;
/** The viewport's height (max-h-160), which "fit" fits the graph into. */
const VIEWPORT_HEIGHT = 640;

const KIND_ICON: Record<NodeKind, React.ElementType> = {
  tenant: BuildingOfficeIcon,
  collection: FolderIcon,
  bucket: ArchiveBoxIcon,
  backend: ServerStackIcon,
  policy: ShieldCheckIcon,
  principal: IdentificationIcon,
};

/** What each tone means, in the legend, whatever kind of thing wears it. */
const TONE_MEANING: Record<StatusTone, string> = {
  success: "Working",
  progress: "Changing",
  warning: "Needs a look",
  danger: "Failing",
  suspended: "Paused",
  unknown: "Not known",
};

// The box every node is drawn in; an orphan's is dashed and recedes, as
// Argo CD draws a resource nothing owns.
const NODE_BOX =
  "absolute flex items-center gap-2.5 rounded-md border bg-card px-3 text-left text-card-foreground shadow-raised";
const ORPHAN_BOX = "border-dashed bg-muted text-muted-foreground shadow-none";
const DIMMED = "opacity-40";
// The node pointed at, and the curves to its parents and children.
const ACTIVE_BOX = "border-primary";
const LIT_EDGE = "stroke-primary";

export function TopologyGraph({
  graph,
  label,
  onToggle,
}: {
  graph: Graph;
  /** Names the drawing for a screen reader: "Topology of acme". */
  label: string;
  /** Shows or hides what a node's toggle names; none offers no toggle. */
  onToggle?: (key: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [grouped, setGrouped] = useState(true);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [zoom, setZoom] = useState(1);
  // The node under the pointer or holding focus, whose neighbourhood is lit.
  const [active, setActive] = useState<string | null>(null);
  const viewport = useRef<HTMLDivElement>(null);

  const shown = useMemo(
    () => (grouped ? groupSiblings(graph, GROUP_ABOVE, expanded) : graph),
    [graph, grouped, expanded],
  );
  const layout = useMemo(() => layoutGraph(shown), [shown]);
  const matching = useMemo(
    () =>
      new Set(
        shown.nodes.filter((n) => nodeMatches(n, query)).map((n) => n.id),
      ),
    [shown, query],
  );
  const filtering = query.trim() !== "";
  const lit = useMemo(
    () => (active ? neighbourhood(shown, active) : null),
    [shown, active],
  );
  // The lit curves are drawn last, so no unlit one crosses over them.
  const edges = useMemo(
    () =>
      lit
        ? [
            ...layout.edges.filter((e) => !lit.edges.has(edgeKey(e))),
            ...layout.edges.filter((e) => lit.edges.has(edgeKey(e))),
          ]
        : layout.edges,
    [layout, lit],
  );
  const tones = useMemo(() => {
    const present = new Set(shown.nodes.map((n) => n.status?.tone));
    return STATUS_ORDER.filter((t) => present.has(t));
  }, [shown]);
  const orphans = shown.nodes.some((n) => n.orphan);

  // A plain wheel scrolls the viewport, as on any page; with Ctrl or ⌘ it
  // zooms, as a map does. React's wheel listener is passive and could not
  // stop the browser zooming the whole page, so this one is attached here.
  useEffect(() => {
    const el = viewport.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      setZoom((z) => zoomBy(z, e.deltaY < 0 ? 1 : -1));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const fit = () =>
    setZoom(
      fitZoom(layout, {
        width: viewport.current?.clientWidth ?? 0,
        height: VIEWPORT_HEIGHT,
      }),
    );

  const expand = (id: string) => setExpanded((prev) => new Set([...prev, id]));

  return (
    <div className="space-y-3">
      <TableToolbar
        query={query}
        onQueryChange={setQuery}
        searchLabel="Filter the topology"
        placeholder="Filter by name, kind or state…"
        count={{ shown: matching.size, total: shown.nodes.length }}
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              aria-pressed={grouped}
              onClick={() => {
                setGrouped((g) => !g);
                setExpanded(new Set());
              }}
              title={`Fold more than ${GROUP_ABOVE} siblings of one kind into one node`}
            >
              <RectangleGroupIcon className="size-4" />
              Group siblings
            </Button>
            <div
              role="group"
              aria-label="Zoom"
              className="flex items-center gap-1"
            >
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="Zoom out"
                title="Zoom out (Ctrl or ⌘ and scroll)"
                disabled={zoom <= ZOOM_MIN}
                onClick={() => setZoom((z) => zoomBy(z, -1))}
              >
                <MagnifyingGlassMinusIcon className="size-4" />
              </Button>
              <span
                className="w-12 text-center text-xs tabular-nums text-muted-foreground"
                aria-live="polite"
              >
                {zoomPercent(zoom)}
              </span>
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="Zoom in"
                title="Zoom in (Ctrl or ⌘ and scroll)"
                disabled={zoom >= ZOOM_MAX}
                onClick={() => setZoom((z) => zoomBy(z, 1))}
              >
                <MagnifyingGlassPlusIcon className="size-4" />
              </Button>
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="Fit to view"
                title="Fit to view"
                onClick={fit}
              >
                <ArrowsPointingInIcon className="size-4" />
              </Button>
            </div>
          </>
        }
      />

      <div className="relative rounded-lg border bg-background">
        <div
          ref={viewport}
          role="group"
          aria-label={label}
          className="max-h-160 overflow-auto"
        >
          {/* The scrolled size is the drawing's at this zoom, so the scroll
              bars pan exactly as far as there is drawing. */}
          <div
            className="relative"
            style={{
              width: layout.width * zoom,
              height: layout.height * zoom,
            }}
          >
            <div
              className="absolute left-0 top-0 origin-top-left"
              style={{
                width: layout.width,
                height: layout.height,
                transform: `scale(${zoom})`,
              }}
            >
              <svg
                aria-hidden
                width={layout.width}
                height={layout.height}
                className="pointer-events-none absolute inset-0"
              >
                {edges.map((e) => {
                  const key = edgeKey(e);
                  const on = lit?.edges.has(key) ?? false;
                  return (
                    <path
                      key={key}
                      d={e.path}
                      strokeWidth={on ? 2 : 1.5}
                      data-lit={on || undefined}
                      className={cn(
                        "fill-none stroke-border",
                        on && LIT_EDGE,
                        ((filtering &&
                          !(matching.has(e.from) && matching.has(e.to))) ||
                          (lit && !on)) &&
                          DIMMED,
                      )}
                    />
                  );
                })}
              </svg>
              {layout.nodes.map((p) => (
                <TopologyNodeBox
                  key={p.node.id}
                  placed={p}
                  dimmed={
                    (filtering && !matching.has(p.node.id)) ||
                    (lit !== null && !lit.nodes.has(p.node.id))
                  }
                  active={active === p.node.id}
                  onActivate={setActive}
                  onExpand={expand}
                  onToggle={onToggle}
                />
              ))}
            </div>
          </div>
        </div>
        {filtering && matching.size === 0 ? (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-background/80 text-sm text-muted-foreground">
            <p>Nothing here matches “{query.trim()}”.</p>
            <Button variant="outline" size="sm" onClick={() => setQuery("")}>
              Clear filter
            </Button>
          </div>
        ) : null}
      </div>

      <ul
        aria-label="Legend"
        className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground"
      >
        {tones.map((t) => (
          <li key={t} className="inline-flex items-center gap-1.5">
            <StatusIcon tone={t} />
            {TONE_MEANING[t]}
          </li>
        ))}
        {orphans ? (
          <li className="inline-flex items-center gap-1.5">
            <span
              aria-hidden
              className="inline-block h-3 w-5 rounded-sm border border-dashed border-muted-foreground bg-muted"
            />
            Used by nothing
          </li>
        ) : null}
      </ul>
    </div>
  );
}

function TopologyNodeBox({
  placed,
  dimmed,
  active,
  onActivate,
  onExpand,
  onToggle,
}: {
  placed: PlacedNode;
  dimmed: boolean;
  active: boolean;
  /** The node pointed at or focused; null when the pointer or focus leaves. */
  onActivate: (id: string | null) => void;
  onExpand: (id: string) => void;
  onToggle?: (key: string) => void;
}) {
  const { node, x, y } = placed;
  const Icon = KIND_ICON[node.kind];
  const box = {
    left: x,
    top: y,
    width: NODE_WIDTH,
    height: NODE_HEIGHT,
  };
  const body = (
    <>
      <Icon aria-hidden className="size-5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{node.label}</span>
        <span className="block truncate text-xs text-muted-foreground">
          {isGroup(node)
            ? "Select to show each"
            : [KIND_NOUN[node.kind].one, node.detail]
                .filter(Boolean)
                .join(" · ")}
        </span>
      </span>
      {node.status ? (
        <StatusIcon tone={node.status.tone} label={node.status.label} />
      ) : null}
    </>
  );
  const boxClass = cn(
    NODE_BOX,
    HOVER_SURFACE,
    FOCUS_RING,
    node.orphan && ORPHAN_BOX,
    active && ACTIVE_BOX,
    dimmed && DIMMED,
  );
  // On the box itself, so its menu button keeps it lit; focus leaving the
  // box for its own menu keeps it too.
  const activation = {
    onMouseEnter: () => onActivate(node.id),
    onMouseLeave: () => onActivate(null),
    onFocus: () => onActivate(node.id),
    onBlur: (e: React.FocusEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null))
        onActivate(null);
    },
  };

  if (isGroup(node)) {
    return (
      <button
        type="button"
        style={box}
        className={boxClass}
        aria-label={`Show the ${node.label} one by one`}
        onClick={() => onExpand(node.id)}
        data-node={node.id}
        {...activation}
      >
        {body}
      </button>
    );
  }

  const links = node.links ?? [];
  const toggle = onToggle ? node.toggle : undefined;
  const menu = links.length > 0 || toggle !== undefined;
  return (
    <div
      style={box}
      className="absolute"
      data-node={node.id}
      data-active={active || undefined}
      {...activation}
    >
      {node.href ? (
        <Link
          href={node.href}
          className={cn(boxClass, "inset-0", menu && "pr-10")}
        >
          {body}
        </Link>
      ) : (
        <div className={cn(boxClass, "inset-0")}>{body}</div>
      )}
      {menu ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              className="absolute right-2 top-1/2 -translate-y-1/2"
              aria-label={`More for ${node.label}`}
            >
              <EllipsisVerticalIcon className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {toggle && onToggle ? (
              <DropdownMenuItem onSelect={() => onToggle(toggle.key)}>
                {toggle.label}
              </DropdownMenuItem>
            ) : null}
            {links.map((l) => (
              <DropdownMenuItem key={l.href} asChild>
                <Link href={l.href}>{l.label}</Link>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  );
}
