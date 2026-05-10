"use client";

import React, {
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
} from "react";
import { useRouter } from "next/navigation";
import {
  MagnifyingGlassIcon,
  DocumentIcon,
  ChartBarIcon,
  HeartIcon,
  Cog6ToothIcon,
  CommandLineIcon,
  ArrowRightIcon,
  ServerStackIcon,
  BuildingOfficeIcon,
  ClockIcon,
} from "@heroicons/react/24/outline";
import { useActions } from "@/context/ActionsContext";
import { useScope } from "@/context/ScopeContext";
import { tenantClient } from "@/lib/connect/client";
import { API_PAGE_SIZE_MAX } from "@/constants";
interface SearchResult {
  id: string;
  type: "action" | "object" | "nav";
  title: string;
  subtitle?: string;
  icon: React.ElementType;
  shortcut?: string;
  onSelect: () => void;
}

export function CommandPalette() {
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [searchResults, setSearchResults] = useState<SearchResult[]>([]);
  const [isSearching, setIsSearching] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(0);

  const router = useRouter();
  const { actions } = useActions();
  const { tenant: scopedTenant, objectKey: scopedObjectKey } = useScope();
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  // Resolve the URL slug for the active scoped tenant. Tenant proto
  // doesn't expose a slug field today (BACKLOG), so fall back to
  // tenantId — that still routes correctly because TenantLayout
  // accepts both forms and replaces the address bar with the
  // canonical slug once it resolves.
  const scopedTenantSlug = scopedTenant?.tenantId ?? null;

  const tenantScopedNavs: SearchResult[] = useMemo(() => {
    if (!scopedTenantSlug) return [];
    const base = `/tenants/${encodeURIComponent(scopedTenantSlug)}`;
    const label = scopedTenant?.displayName || scopedTenantSlug;
    return [
      {
        id: "nav-tenant-overview",
        type: "nav",
        title: `Open ${label}`,
        subtitle: "Tenant overview, identity + quick links",
        icon: BuildingOfficeIcon,
        shortcut: "G T",
        onSelect: () => router.push(base),
      },
      {
        id: "nav-tenant-buckets",
        type: "nav",
        title: `Buckets in ${label}`,
        subtitle: "S3 buckets owned by this tenant",
        icon: ServerStackIcon,
        shortcut: "G B",
        onSelect: () => router.push(`${base}/buckets`),
      },
      {
        id: "nav-tenant-object-keys",
        type: "nav",
        title: `Object Keys in ${label}`,
        subtitle: "Tenant-scoped namespaces routed to a bucket",
        icon: ServerStackIcon,
        shortcut: "G K",
        onSelect: () => router.push(`${base}/object-keys`),
      },
      {
        id: "nav-tenant-policies",
        type: "nav",
        title: `Policies in ${label}`,
        subtitle: "Effective Cedar graph for this tenant",
        icon: CommandLineIcon,
        onSelect: () => router.push(`${base}/policies`),
      },
      {
        id: "nav-tenant-audit",
        type: "nav",
        title: `Audit log for ${label}`,
        subtitle: "Tenant-scoped mutation log",
        icon: ClockIcon,
        onSelect: () => router.push(`${base}/audit-log`),
      },
    ];
  }, [router, scopedTenantSlug, scopedTenant?.displayName]);

  const staticNavs: SearchResult[] = useMemo(
    () => [
      {
        id: "nav-tenants",
        type: "nav",
        title: "Resources",
        subtitle: "Tenant index — gateway to all resource subtrees",
        icon: BuildingOfficeIcon,
        shortcut: "G R",
        onSelect: () => router.push("/tenants"),
      },
      {
        id: "nav-policies",
        type: "nav",
        title: "Policies",
        subtitle: "System-wide Cedar editor",
        icon: CommandLineIcon,
        shortcut: "G P",
        onSelect: () => router.push("/policies"),
      },
      // Cross-tenant Object Explorer is gone — flat /objects page
      // was deleted in Phase 5. Object listing now requires an
      // ObjectKey scope (lives under /tenants/<id>/object-keys/
      // <name>/objects). The cross-tenant /buckets and /object-keys
      // entries below stay because their RPCs accept empty parents.
      {
        id: "nav-buckets",
        type: "nav",
        title: "Buckets (cross-tenant)",
        subtitle: "Platform-admin index of every bucket",
        icon: ServerStackIcon,
        onSelect: () => router.push("/buckets"),
      },
      {
        id: "nav-object-keys",
        type: "nav",
        title: "Object Keys (cross-tenant)",
        subtitle: "Platform-admin index of every ObjectKey",
        icon: ServerStackIcon,
        onSelect: () => router.push("/object-keys"),
      },
      {
        id: "nav-upload",
        type: "nav",
        title: "Upload Assets",
        subtitle: "Upload new files",
        icon: DocumentIcon,
        shortcut: "G U",
        onSelect: () => router.push("/upload"),
      },
      // /trash and /object-tags removed in Phase 5. Trash is now
      // a per-ObjectKey tab (/tenants/.../object-keys/<name>/trash).
      // Object Tags drop entirely — they were a holdover taxonomy
      // surface; per the BACKLOG they'll come back as a label
      // filter on the Objects tab once the cross-bucket index lands.
      {
        id: "nav-stats",
        type: "nav",
        title: "System Stats",
        subtitle: "View real-time metrics and charts",
        icon: ChartBarIcon,
        shortcut: "G S",
        onSelect: () => router.push("/health"),
      },
      {
        id: "nav-health",
        type: "nav",
        title: "Health Check",
        subtitle: "Monitor infrastructure status",
        icon: HeartIcon,
        shortcut: "G H",
        onSelect: () => router.push("/health"),
      },
      {
        id: "nav-config",
        type: "nav",
        title: "Configuration",
        subtitle: "View environment and API settings",
        icon: Cog6ToothIcon,
        shortcut: "G C",
        onSelect: () => router.push("/config"),
      },
    ],
    [router],
  );

  // Default-state list (palette open, no query): show the tenant
  // jumps first when a tenant is in scope, then the global static
  // navs. Searching merges both pools; see performSearch below.
  const defaultNavs = useMemo(
    () => [...tenantScopedNavs, ...staticNavs],
    [tenantScopedNavs, staticNavs],
  );

  const performSearch = useCallback(
    async (searchQuery: string) => {
      if (!searchQuery) {
        setSearchResults([]);
        return;
      }

      setIsSearching(true);

      // 1. First, set local results immediately
      const actionResults: SearchResult[] = actions
        .filter(
          (a) =>
            a.label.toLowerCase().includes(searchQuery.toLowerCase()) ||
            (a.description &&
              a.description.toLowerCase().includes(searchQuery.toLowerCase())),
        )
        .map((a) => ({
          id: `action-${a.id}`,
          type: "action",
          title: a.label,
          subtitle: a.description,
          icon: a.icon || CommandLineIcon,
          shortcut: a.shortcut,
          onSelect: () => a.perform(),
        }));

      // Tenant-scoped jumps + global static navs both in the search
      // pool. Tenant-scoped entries naturally rank higher because
      // their labels include the active tenant name (operators
      // typing "buckets" while in tenant T see "Buckets in T" first).
      const lowerQuery = searchQuery.toLowerCase();
      const navResults: SearchResult[] = [
        ...tenantScopedNavs,
        ...staticNavs,
      ].filter(
        (n) =>
          n.title.toLowerCase().includes(lowerQuery) ||
          (n.subtitle && n.subtitle.toLowerCase().includes(lowerQuery)),
      );

      setSearchResults([...actionResults, ...navResults]);
      setSelectedIndex(0);

      try {
        // Tenant search via ListTenants — once the proto exposes a
        // slug field, this should switch to slug-form URLs. Until
        // then tenantId routes correctly through the layout's
        // resolver (which canonicalises UUID→slug on landing).
        const tenantResponse = await tenantClient.listTenants({
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
          filter: searchQuery,
        });

        const tenantResults: SearchResult[] = tenantResponse.tenants.map(
          (t) => ({
            id: `tenant-${t.tenantId}`,
            type: "nav",
            title: t.displayName || t.tenantId,
            subtitle: `Open tenant — id ${t.tenantId}`,
            icon: BuildingOfficeIcon,
            onSelect: () =>
              router.push(`/tenants/${encodeURIComponent(t.tenantId)}`),
          }),
        );

        // Object search left out — ListObjects requires backend +
        // bucket scoping today. Once the data plane exposes a
        // tenant-wide cross-bucket search, surface object hits
        // here. `scopedObjectKey` retained so the dependency array
        // re-fires when scope changes (placeholder for that future
        // wiring).
        void scopedObjectKey;

        if (tenantResults.length > 0) {
          setSearchResults((prev) => [...prev, ...tenantResults]);
        }
      } catch (err) {
        console.error("Command Palette API search failed", err);
      } finally {
        setIsSearching(false);
      }
    },
    [actions, staticNavs, tenantScopedNavs, router, scopedObjectKey],
  );

  // Debounced search
  useEffect(() => {
    const timer = setTimeout(() => {
      performSearch(query);
    }, 200);
    return () => clearTimeout(timer);
  }, [query, performSearch]);

  const toggle = useCallback(() => {
    setIsOpen((open) => !open);
    setQuery("");
    setSearchResults([]);
    setSelectedIndex(0);
  }, []);

  useEffect(() => {
    let lastKey = "";
    let lastKeyTime = 0;

    const handleKeyDown = (e: KeyboardEvent) => {
      // Cmd+K
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        toggle();
        return;
      }

      if (e.key === "Escape") {
        setIsOpen(false);
        return;
      }

      if (isOpen) {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setSelectedIndex(
            (i) => (i + 1) % (searchResults.length || defaultNavs.length),
          );
        } else if (e.key === "ArrowUp") {
          e.preventDefault();
          setSelectedIndex(
            (i) =>
              (i - 1 + (searchResults.length || defaultNavs.length)) %
              (searchResults.length || defaultNavs.length),
          );
        } else if (e.key === "Enter") {
          e.preventDefault();
          const results =
            searchResults.length > 0 ? searchResults : defaultNavs;
          const selected = results[selectedIndex];
          if (selected) {
            selected.onSelect();
            setIsOpen(false);
          }
        }
        return;
      }

      // Global Shortcuts (g + ...). Tenant-scoped jumps win over
      // generic ones — that way "G B" goes to "Buckets in <scope>"
      // when a tenant is active and to the cross-tenant index when
      // it isn't.
      if (
        !isOpen &&
        !["INPUT", "TEXTAREA"].includes((e.target as HTMLElement).tagName)
      ) {
        const now = Date.now();
        const key = e.key.toLowerCase();

        if (lastKey === "g" && now - lastKeyTime < 500) {
          const pool = [...tenantScopedNavs, ...staticNavs];
          const nav = pool.find((n) => n.shortcut?.toLowerCase().endsWith(key));
          if (nav) {
            e.preventDefault();
            nav.onSelect();
          }
          lastKey = "";
        } else if (key === "g") {
          lastKey = "g";
          lastKeyTime = now;
        } else {
          lastKey = "";
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [
    toggle,
    staticNavs,
    tenantScopedNavs,
    defaultNavs,
    isOpen,
    searchResults,
    selectedIndex,
  ]);

  // Auto-scroll to selected index
  useEffect(() => {
    if (scrollContainerRef.current) {
      const selectedElement = scrollContainerRef.current.children[
        selectedIndex
      ] as HTMLElement;
      if (selectedElement) {
        selectedElement.scrollIntoView({ block: "nearest" });
      }
    }
  }, [selectedIndex]);

  if (!isOpen) return null;

  const displayedResults =
    searchResults.length > 0 || query !== "" ? searchResults : defaultNavs;

  return (
    <div className="fixed inset-0 z-[200] flex items-start justify-center pt-[15vh] px-4">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-xl transition-opacity animate-fade-in"
        onClick={() => setIsOpen(false)}
      />

      {/* Palette Container */}
      <div className="relative w-full max-w-2xl bg-[#0A0C10]/95 rounded-[32px] overflow-hidden shadow-[0_0_50px_rgba(0,0,0,0.5)] border border-white/10 animate-bounce-in">
        <div className="absolute top-0 left-0 w-full h-px bg-gradient-to-r from-transparent via-indigo-500 to-transparent opacity-50" />

        <div className="p-6 border-b border-white/5 flex items-center gap-4">
          <div className="relative">
            {isSearching ? (
              <div className="w-6 h-6 border-2 border-indigo-500/20 border-t-indigo-500 rounded-full animate-spin" />
            ) : (
              <MagnifyingGlassIcon className="w-6 h-6 text-indigo-400" />
            )}
          </div>
          <input
            autoFocus
            type="text"
            placeholder="Type to search objects, run actions, or navigate..."
            className="bg-transparent border-none focus:ring-0 text-white placeholder-slate-600 w-full text-xl font-medium outline-none"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="flex items-center gap-2">
            <span className="px-2 py-1 rounded-lg bg-white/5 border border-white/10 text-xs font-bold text-slate-500 uppercase tracking-wider">
              ESC
            </span>
          </div>
        </div>

        <div
          className="max-h-[60vh] overflow-y-auto p-3 scrollbar-hide"
          ref={scrollContainerRef}
        >
          {displayedResults.length > 0 ? (
            displayedResults.map((result, index) => (
              <button
                key={result.id}
                onClick={() => {
                  result.onSelect();
                  setIsOpen(false);
                }}
                className={`w-full flex items-center justify-between p-4 rounded-2xl transition-all border group relative ${
                  index === selectedIndex
                    ? "bg-indigo-500/10 border-indigo-500/30 shadow-lg"
                    : "bg-transparent border-transparent hover:bg-white/[0.02]"
                }`}
                onMouseEnter={() => setSelectedIndex(index)}
              >
                {index === selectedIndex && (
                  <div className="absolute left-0 w-1 h-6 bg-indigo-500 rounded-r-full" />
                )}
                <div className="flex items-center gap-4 text-left">
                  <div
                    className={`w-12 h-12 rounded-xl flex items-center justify-center transition-colors ${
                      index === selectedIndex
                        ? "bg-indigo-500/20 text-indigo-400"
                        : "bg-white/5 text-slate-500 group-hover:bg-white/10"
                    }`}
                  >
                    <result.icon className="w-6 h-6" />
                  </div>
                  <div className="min-w-0">
                    <div
                      className={`text-sm font-bold truncate ${
                        index === selectedIndex
                          ? "text-white"
                          : "text-slate-300"
                      }`}
                    >
                      {result.title}
                    </div>
                    {result.subtitle && (
                      <div className="text-xs text-slate-500 truncate mt-0.5 font-medium uppercase tracking-wider">
                        {result.subtitle}
                      </div>
                    )}
                  </div>
                </div>

                <div className="flex items-center gap-3">
                  {result.shortcut && (
                    <div className="flex gap-1">
                      {result.shortcut.split(" ").map((s) => (
                        <span
                          key={s}
                          className="px-2 py-1 rounded bg-white/5 border border-white/10 text-xs font-mono text-slate-600"
                        >
                          {s}
                        </span>
                      ))}
                    </div>
                  )}
                  {index === selectedIndex && (
                    <ArrowRightIcon className="w-4 h-4 text-indigo-500 animate-slide-right" />
                  )}
                </div>
              </button>
            ))
          ) : query !== "" && !isSearching ? (
            <div className="py-20 text-center space-y-4">
              <div className="w-16 h-16 rounded-3xl bg-white/5 flex items-center justify-center mx-auto border border-dashed border-white/10">
                <MagnifyingGlassIcon className="w-8 h-8 text-slate-700" />
              </div>
              <div>
                <p className="text-sm font-bold text-slate-400">
                  No matches found for &ldquo;{query}&rdquo;
                </p>
                <p className="text-xs text-slate-600 uppercase tracking-wider mt-1">
                  Try searching for something else
                </p>
              </div>
            </div>
          ) : null}
        </div>

        <div className="p-4 bg-white/[0.02] border-t border-white/5 flex items-center justify-between text-xs font-bold text-slate-600 uppercase tracking-wider">
          <div className="flex gap-6">
            <span className="flex items-center gap-2">
              <kbd className="px-1.5 py-0.5 rounded bg-white/5 border border-white/10 text-slate-400">
                ↑↓
              </kbd>
              Navigate
            </span>
            <span className="flex items-center gap-2">
              <kbd className="px-1.5 py-0.5 rounded bg-white/5 border border-white/10 text-slate-400">
                Enter
              </kbd>
              Select
            </span>
          </div>
          <div className="flex items-center gap-4">
            <span className="text-indigo-500/40">PALADIN v2.0 Global Index</span>
          </div>
        </div>
      </div>
    </div>
  );
}
