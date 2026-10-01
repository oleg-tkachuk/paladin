"use client";

import {
  MagnifyingGlassIcon,
  FunnelIcon,
  ArrowPathIcon,
  DocumentIcon,
  EllipsisVerticalIcon,
  CheckIcon,
  ChevronDownIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { Select } from "@/components/ui/Select";
import { Dropdown } from "@/components/ui/Dropdown";

interface SavedView {
  name: string;
  filters: {
    search?: string;
    status?: string;
    recursive?: boolean;
  };
}

/**
 * The object-list filter values, collapsed into one object. `tag` is the
 * selected "key=value" facet (undefined = none); `status` is undefined when
 * unfiltered.
 */
export interface FilterState {
  search: string;
  status: string | undefined;
  tag: string | undefined;
  recursive: boolean;
}

interface ObjectsFilterBarProps {
  /** The four filter values, bundled. */
  filter: FilterState;
  /** Apply a single-field patch — the bar always changes one field at a time. */
  onFilterChange: (patch: Partial<FilterState>) => void;
  /** Distinct "key=value" pairs seen across loaded objects. */
  tagOptions: string[];
  visibleColumns: Set<string>;
  onToggleColumn: (col: string) => void;
  savedViews: SavedView[];
  onApplyView: (view: SavedView) => void;
  onDeleteView: (name: string) => void;
  onSaveView: () => void;
  loading: boolean;
  onRefresh: () => void;
}

const COLUMN_OPTIONS = [
  { id: "key", label: "Name / ID" },
  { id: "object_tag", label: "ObjectTag" },
  { id: "mime", label: "MIME Type" },
  { id: "size", label: "Size" },
  { id: "status", label: "Sync Status" },
  { id: "created", label: "Initialization" },
  { id: "actions", label: "Operations" },
];

const STATUS_OPTIONS = [
  { value: "all", label: "All Statuses" },
  { value: "PENDING", label: "Pending" },
  { value: "AVAILABLE", label: "Available" },
  { value: "FAILED", label: "Failed" },
  { value: "DELETED", label: "Deleted" },
];

export function ObjectsFilterBar({
  filter,
  onFilterChange,
  tagOptions,
  visibleColumns,
  onToggleColumn,
  savedViews,
  onApplyView,
  onDeleteView,
  onSaveView,
  loading,
  onRefresh,
}: ObjectsFilterBarProps) {
  // Re-derive the per-field values + setters from the bundled filter so the
  // markup below stays unchanged. Each setter emits a single-field patch.
  const { search, status, tag, recursive } = filter;
  const onSearchChange = (value: string) => onFilterChange({ search: value });
  const onStatusChange = (value: string | undefined) =>
    onFilterChange({ status: value });
  const onTagChange = (value: string | undefined) =>
    onFilterChange({ tag: value });
  const onRecursiveChange = (value: boolean) =>
    onFilterChange({ recursive: value });
  return (
    <div className="flex flex-wrap items-center gap-4 bg-surface/30 border border-border rounded-2xl p-4">
      <div className="flex-1 min-w-[240px] relative group">
        <MagnifyingGlassIcon className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground group-focus-within:text-primary transition-colors" />
        <input
          type="text"
          placeholder="Search objects..."
          className="w-full bg-foreground/5 border border-border rounded-xl pl-10 pr-4 py-2 text-sm text-foreground focus:outline-none focus:border-primary/50 focus:bg-foreground/[0.08] transition-all"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
        />
      </div>

      <div className="flex items-center gap-2">
        <FunnelIcon className="w-4 h-4 text-muted-foreground" />

        <div className="flex items-center gap-2 px-3 py-2 bg-foreground/5 border border-border rounded-xl">
          <input
            id="recursive-toggle"
            type="checkbox"
            className="rounded border-border bg-foreground/5 text-primary focus:ring-offset-0 focus:ring-primary focus:ring-opacity-50 cursor-pointer w-3.5 h-3.5"
            checked={recursive}
            onChange={(e) => onRecursiveChange(e.target.checked)}
          />
          <label
            htmlFor="recursive-toggle"
            className="text-xs font-semibold text-muted-foreground uppercase tracking-wider cursor-pointer select-none"
          >
            Recursive
          </label>
        </div>

        <Select
          aria-label="Filter by status"
          options={STATUS_OPTIONS}
          value={status || "all"}
          onChange={(val) => onStatusChange(val === "all" ? undefined : val)}
        />

        {tagOptions.length > 0 && (
          <Select
            aria-label="Filter by tag"
            options={[
              { value: "all", label: "All Tags" },
              ...tagOptions.map((t) => ({
                value: t,
                label: t.replace("=", ": "),
              })),
            ]}
            value={tag || "all"}
            onChange={(val) => onTagChange(val === "all" ? undefined : val)}
          />
        )}

        <Dropdown align="right" width="w-72">
          <Dropdown.Trigger className="flex items-center gap-2 px-3 py-2 bg-foreground/5 border border-border rounded-xl text-muted-foreground hover:text-foreground transition-all">
            <DocumentIcon className="w-4 h-4" />
            <span className="text-xs font-bold uppercase tracking-wider">
              Views
            </span>
            <ChevronDownIcon className="w-3 h-3 ml-1 opacity-50" />
          </Dropdown.Trigger>
          <Dropdown.Menu className="p-2 space-y-1">
            {savedViews.length === 0 && (
              <div className="p-4 text-center">
                <p className="text-xs text-muted-foreground font-semibold uppercase tracking-wider">
                  No Saved Views
                </p>
              </div>
            )}
            {savedViews.map((view) => (
              <Dropdown.Item
                key={view.name}
                className="group/view p-0"
                // On the item, which takes Enter and Space; a click handler on
                // an inner div applied the view for a mouse only.
                onClick={() => onApplyView(view)}
              >
                <div className="w-full flex items-center justify-between gap-3 px-4 py-3 text-xs font-bold uppercase tracking-wider text-foreground hover:text-foreground hover:bg-foreground/5 transition-all rounded-xl">
                  <div className="flex-1 cursor-pointer">{view.name}</div>
                  <button
                    type="button"
                    aria-label={`Delete view ${view.name}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      onDeleteView(view.name);
                    }}
                    onKeyDown={(e) => e.stopPropagation()}
                    className="opacity-0 group-hover/view:opacity-100 focus-visible:opacity-100 p-1 hover:text-destructive transition-all"
                  >
                    <TrashIcon className="w-3 h-3" />
                  </button>
                </div>
              </Dropdown.Item>
            ))}
            <div className="h-px bg-foreground/5 my-1" />
            <Dropdown.Item onClick={onSaveView}>
              <div className="w-full flex items-center gap-3 px-4 py-3 text-xs font-bold uppercase tracking-wider text-primary hover:text-primary hover:bg-primary/5 transition-all rounded-xl">
                <PlusIcon className="w-4 h-4" />
                Save Current View
              </div>
            </Dropdown.Item>
          </Dropdown.Menu>
        </Dropdown>

        <Dropdown align="right" width="w-56">
          <Dropdown.Trigger className="flex items-center gap-2 px-3 py-2 bg-foreground/5 border border-border rounded-xl text-muted-foreground hover:text-foreground transition-all">
            <EllipsisVerticalIcon className="w-4 h-4" />
            <span className="text-xs font-bold uppercase tracking-wider">
              Layout
            </span>
          </Dropdown.Trigger>
          <Dropdown.Menu className="p-2 space-y-1">
            <div className="px-4 py-2 text-xs font-semibold text-muted-foreground uppercase tracking-wider border-b border-border mb-1">
              Table Columns
            </div>
            {COLUMN_OPTIONS.map((col) => (
              <Dropdown.Item
                key={col.id}
                onClick={() => onToggleColumn(col.id)}
                className="p-0"
                closeOnClick={false}
              >
                <div className="w-full flex items-center justify-between px-4 py-2.5 text-xs font-semibold text-foreground hover:text-foreground hover:bg-foreground/5 rounded-lg transition-all">
                  {col.label}
                  {visibleColumns.has(col.id) && (
                    <CheckIcon className="w-3.5 h-3.5 text-primary" />
                  )}
                </div>
              </Dropdown.Item>
            ))}
          </Dropdown.Menu>
        </Dropdown>

        <button
          onClick={() => onRefresh()}
          title="Refresh object list from backend"
          className="p-2 text-muted-foreground hover:text-foreground bg-foreground/5 border border-border rounded-xl transition-colors"
        >
          <ArrowPathIcon
            className={`w-4 h-4 ${loading ? "animate-spin" : ""}`}
          />
        </button>
      </div>
    </div>
  );
}
