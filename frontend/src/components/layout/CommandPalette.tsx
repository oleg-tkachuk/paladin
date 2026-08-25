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
import {
  tenantClient,
  bucketClient,
  collectionClient,
  backendClient,
} from "@/lib/connect/client";
import { ArchiveBoxIcon, TagIcon } from "@heroicons/react/24/outline";
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
  const { tenant: scopedTenant, collection: scopedCollection } = useScope();
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  // Resolve the URL handle for the active scoped tenant. Prefer
  // slug; fall back to UUID for backwards compat with deploys that
  // pre-date the proto-slug field. TenantLayout's resolver still
  // canonicalises UUID URLs to slug on landing if both present.
  const scopedTenantSlug = scopedTenant?.slug || scopedTenant?.tenantId || null;

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
        id: "nav-tenant-collections",
        type: "nav",
        title: `Collections in ${label}`,
        subtitle: "Tenant-scoped namespaces routed to a bucket",
        icon: ServerStackIcon,
        shortcut: "G K",
        onSelect: () => router.push(`${base}/collections`),
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
      // Collection scope (lives under /tenants/<id>/collections/
      // <name>/objects). The cross-tenant /buckets and /collections
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
        id: "nav-collections",
        type: "nav",
        title: "Collections (cross-tenant)",
        subtitle: "Platform-admin index of every Collection",
        icon: ServerStackIcon,
        onSelect: () => router.push("/collections"),
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
      // a per-Collection tab (/tenants/.../collections/<name>/trash).
      // Object Tags drop entirely — they were a holdover taxonomy
      // surface; per the BACKLOG they'll come back as a label
      // filter on the Objects tab once the cross-bucket index lands.
      {
        id: "nav-stats",
        type: "nav",
        title: "Platform Stats",
        subtitle: "Tenant / storage / object census across the fleet",
        icon: ChartBarIcon,
        onSelect: () => router.push("/stats"),
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
        // Parallel-fan-out global resource search. Each RPC is
        // best-effort: a slow / failing one doesn't block the others
        // from populating the palette. Result count caps per source
        // keep the palette scannable.
        // The plane's `filter` is a CEL expression, not a search string.
        // Passing the raw query made every keystroke a compile error, and the
        // .catch() below turned that into an empty result — a palette that
        // silently found nothing rather than one that looked broken. It went
        // unnoticed while ListTenants ignored the field and returned
        // everything.
        const escaped = searchQuery.trim().replace(/["\\]/g, "\\$&");
        const tenantFilter = escaped
          ? `slug.startsWith("${escaped}") || display_name.startsWith("${escaped}")`
          : "";

        const [tenantRes, bucketRes, okRes, backendRes] = await Promise.all([
          tenantClient
            .listTenants({
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter: tenantFilter,
            })
            .catch(() => ({ tenants: [] })),
          bucketClient
            .listBuckets({
              parent: "",
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter: "",
              ownerTenantId: "",
            })
            .catch(() => ({ buckets: [] })),
          collectionClient
            .listCollections({
              parent: "",
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter: "",
            })
            .catch(() => ({ collections: [] })),
          backendClient
            .listBackends({
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter: "",
            })
            .catch(() => ({ backends: [] })),
        ]);

        const q = searchQuery.toLowerCase();
        const matches = (s: string) => s.toLowerCase().includes(q);

        const tenantResults: SearchResult[] = tenantRes.tenants
          .filter(
            (t) =>
              !q ||
              matches(t.slug || "") ||
              matches(t.displayName || "") ||
              matches(t.tenantId),
          )
          .slice(0, 5)
          .map((t) => {
            const handle = t.slug || t.tenantId;
            return {
              id: `tenant-${t.tenantId}`,
              type: "nav",
              title: t.displayName || handle,
              subtitle: `Tenant · ${t.slug ? `slug ${t.slug}` : `id ${t.tenantId}`}`,
              icon: BuildingOfficeIcon,
              onSelect: () =>
                router.push(`/tenants/${encodeURIComponent(handle)}`),
            };
          });

        const tenantSlugByID = new Map(
          tenantRes.tenants.map((t) => [t.tenantId, t.slug || t.tenantId]),
        );

        const backendResults: SearchResult[] = backendRes.backends
          .filter(
            (b) =>
              !q ||
              matches(b.backendId) ||
              matches(b.displayName || "") ||
              matches(b.region || ""),
          )
          .slice(0, 4)
          .map((b) => ({
            id: `backend-${b.backendId}`,
            type: "nav",
            title: b.displayName || b.backendId,
            subtitle: `Storage backend · ${b.backendId}`,
            icon: ServerStackIcon,
            onSelect: () =>
              router.push(
                `/storage-backends/${encodeURIComponent(b.backendId)}`,
              ),
          }));

        const bucketResults: SearchResult[] = bucketRes.buckets
          .filter(
            (b) =>
              !q ||
              matches(b.bucketId) ||
              matches(b.displayName || "") ||
              matches(b.backendId),
          )
          .slice(0, 5)
          .map((b) => ({
            id: `bucket-${b.backendId}-${b.bucketId}`,
            type: "nav",
            title: b.displayName || b.bucketId,
            subtitle: `Bucket · ${b.backendId}/${b.bucketId}`,
            icon: ArchiveBoxIcon,
            onSelect: () =>
              router.push(
                `/storage-backends/${encodeURIComponent(b.backendId)}/buckets/${encodeURIComponent(b.bucketId)}`,
              ),
          }));

        const okResults: SearchResult[] = okRes.collections
          .filter(
            (o) => !q || matches(o.collection) || matches(o.displayName || ""),
          )
          .slice(0, 5)
          .map((o) => {
            const tslug = tenantSlugByID.get(o.tenantId) || o.tenantId;
            return {
              id: `collection-${o.tenantId}-${o.collection}`,
              type: "nav",
              title: o.displayName || o.collection,
              subtitle: `Collection · ${tslug}/${o.collection}`,
              icon: TagIcon,
              onSelect: () =>
                router.push(
                  `/tenants/${encodeURIComponent(tslug)}/collections/${encodeURIComponent(o.collection)}/objects`,
                ),
            };
          });

        // scopedCollection retained for dep-array re-fire when scope
        // changes; once ListObjects offers cross-tenant search,
        // surface object hits here too.
        void scopedCollection;

        const combined = [
          ...tenantResults,
          ...backendResults,
          ...bucketResults,
          ...okResults,
        ];
        if (combined.length > 0) {
          setSearchResults((prev) => [...prev, ...combined]);
        }
      } catch (err) {
        console.error("Command Palette API search failed", err);
      } finally {
        setIsSearching(false);
      }
    },
    [actions, staticNavs, tenantScopedNavs, router, scopedCollection],
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
            {/* Hint at scope rather than versioning — version lives in
                the topbar build-info pill. Empty scope = global; a
                tenant in scope reads as "tenant: <slug>" so the
                operator sees why scoped jumps appear up top. */}
            <span className="text-indigo-500/40">
              {scopedTenantSlug ? (
                <>
                  Scope:{" "}
                  <span className="text-indigo-300/70 font-mono normal-case tracking-normal">
                    {scopedTenantSlug}
                  </span>
                </>
              ) : (
                "Global"
              )}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
