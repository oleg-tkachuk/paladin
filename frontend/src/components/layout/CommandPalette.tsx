"use client";

import React, {
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
} from "react";
import { useRouter } from "next/navigation";
import { searchFilter } from "@/lib/cel";
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
import { canUseAdminPlane } from "@/constants/roles";
import { useAuth } from "@/context/AuthContext";
import { routeNeedsAdminPlane } from "@/lib/adminPlaneRoutes";
interface SearchResult {
  id: string;
  type: "action" | "object" | "nav";
  title: string;
  subtitle?: string;
  icon: React.ElementType;
  shortcut?: string;
  /** Where selecting it goes, so a route the principal cannot use is left out. */
  href?: string;
  onSelect: () => void;
}

export function CommandPalette() {
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [searchResults, setSearchResults] = useState<SearchResult[]>([]);
  const [isSearching, setIsSearching] = useState(false);
  // Sources whose list call failed on the last search. Empty on success, and
  // reset by the early return for an empty query below.
  const [failedSources, setFailedSources] = useState<string[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(0);

  const router = useRouter();
  const { actions } = useActions();
  // A principal without the admin audience: its jumps would land on the
  // no-admin-role card, and every resource search source is admin-plane.
  const { user } = useAuth();
  const adminPlane = !user || canUseAdminPlane(user.roles);
  const reachable = useCallback(
    (r: SearchResult) => adminPlane || !r.href || !routeNeedsAdminPlane(r.href),
    [adminPlane],
  );
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
        href: base,
        onSelect: () => router.push(base),
      },
      {
        id: "nav-tenant-buckets",
        type: "nav",
        title: `Buckets in ${label}`,
        subtitle: "S3 buckets owned by this tenant",
        icon: ServerStackIcon,
        shortcut: "G B",
        href: `${base}/buckets`,
        onSelect: () => router.push(`${base}/buckets`),
      },
      {
        id: "nav-tenant-collections",
        type: "nav",
        title: `Collections in ${label}`,
        subtitle: "Tenant-scoped namespaces routed to a bucket",
        icon: ServerStackIcon,
        shortcut: "G K",
        href: `${base}/collections`,
        onSelect: () => router.push(`${base}/collections`),
      },
      {
        id: "nav-tenant-policies",
        type: "nav",
        title: `Policies in ${label}`,
        subtitle: "Effective Cedar graph for this tenant",
        icon: CommandLineIcon,
        href: `${base}/policies`,
        onSelect: () => router.push(`${base}/policies`),
      },
      {
        id: "nav-tenant-audit",
        type: "nav",
        title: `Audit log for ${label}`,
        subtitle: "Tenant-scoped mutation log",
        icon: ClockIcon,
        href: `${base}/audit-log`,
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
        href: "/tenants",
        onSelect: () => router.push("/tenants"),
      },
      {
        id: "nav-policies",
        type: "nav",
        title: "Policies",
        subtitle: "System-wide Cedar editor",
        icon: CommandLineIcon,
        shortcut: "G P",
        href: "/policies",
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
        href: "/buckets",
        onSelect: () => router.push("/buckets"),
      },
      {
        id: "nav-collections",
        type: "nav",
        title: "Collections (cross-tenant)",
        subtitle: "Platform-admin index of every Collection",
        icon: ServerStackIcon,
        href: "/collections",
        onSelect: () => router.push("/collections"),
      },
      {
        id: "nav-upload",
        type: "nav",
        title: "Upload Assets",
        subtitle: "Upload new files",
        icon: DocumentIcon,
        shortcut: "G U",
        href: "/upload",
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
        href: "/stats",
        onSelect: () => router.push("/stats"),
      },
      {
        id: "nav-health",
        type: "nav",
        title: "Health Check",
        subtitle: "Monitor infrastructure status",
        icon: HeartIcon,
        shortcut: "G H",
        href: "/health",
        onSelect: () => router.push("/health"),
      },
      {
        id: "nav-config",
        type: "nav",
        title: "Configuration",
        subtitle: "View environment and API settings",
        icon: Cog6ToothIcon,
        shortcut: "G C",
        href: "/config",
        onSelect: () => router.push("/config"),
      },
    ],
    [router],
  );

  // Default-state list (palette open, no query): show the tenant
  // jumps first when a tenant is in scope, then the global static
  // navs. Searching merges both pools; see performSearch below.
  const defaultNavs = useMemo(
    () => [...tenantScopedNavs, ...staticNavs].filter(reachable),
    [tenantScopedNavs, staticNavs, reachable],
  );

  const performSearch = useCallback(
    async (searchQuery: string) => {
      if (!searchQuery) {
        setSearchResults([]);
        setFailedSources([]);
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
          reachable(n) &&
          (n.title.toLowerCase().includes(lowerQuery) ||
            (n.subtitle && n.subtitle.toLowerCase().includes(lowerQuery))),
      );

      setSearchResults([...actionResults, ...navResults]);
      setSelectedIndex(0);

      if (!adminPlane) {
        setFailedSources([]);
        setIsSearching(false);
        return;
      }

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
        // ONE filter for all four, built by searchFilter().
        //
        // What was here: a disjunction over slug and display_name for tenants,
        // and `filter: ""` for the other three. Both halves were wrong in the
        // same way. A disjunction pushes nothing into SQL — the server's
        // pushdown descends `&&` only — so it read one page and filtered it in
        // memory; and the empty filters read a page and left the narrowing to
        // the browser. Either way the palette searched the FIRST page of each
        // resource, so a tenant sorting past the ceiling could not be found by
        // typing its name, and nothing on screen said the list was cut.
        //
        // The derived `search` field is one conjunct, so it pushes down, and it
        // covers what this palette matches: the tenant id, the bucket's backend
        // id, a backend's region. Those were added to the field for this
        // caller — moving the search to the server without them would have
        // quietly dropped the cases people use it for.
        const filter = searchFilter(searchQuery);

        // Which sources answered, and which did not.
        //
        // The per-source .catch() is right — one slow or broken plane must not
        // take the whole palette down — but returning an empty list from it
        // made a failure indistinguishable from "nothing matched". An operator
        // typing a bucket name they can see in another tab was told, in
        // silence, that it does not exist. The comment a few lines up already
        // described this failure for the raw-query case and it was never fixed
        // for the transport case.
        const failed: string[] = [];
        const fellOver =
          <T,>(source: string, empty: T) =>
          (): T => {
            failed.push(source);
            return empty;
          };

        const [tenantRes, bucketRes, okRes, backendRes] = await Promise.all([
          tenantClient
            .listTenants({
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter,
            })
            .catch(fellOver("tenants", { tenants: [] })),
          bucketClient
            .listBuckets({
              parent: "",
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter,
              ownerTenantId: "",
            })
            .catch(fellOver("buckets", { buckets: [] })),
          collectionClient
            .listCollections({
              parent: "",
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter,
            })
            .catch(fellOver("collections", { collections: [] })),
          backendClient
            .listBackends({
              page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
              filter,
            })
            .catch(fellOver("backends", { backends: [] })),
        ]);
        setFailedSources(failed);

        // No client-side narrowing. The four calls above are filtered by the
        // server, and a second definition of "matches" in the browser can only
        // hide rows the API deliberately returned. It already had: the backend
        // search field covers the endpoint, and this filter did not, so a match
        // on an endpoint came back from the plane and was dropped here.
        const tenantResults: SearchResult[] = tenantRes.tenants
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
    [
      actions,
      staticNavs,
      tenantScopedNavs,
      router,
      scopedCollection,
      reachable,
      adminPlane,
    ],
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
      <div className="relative w-full max-w-2xl bg-background/95 rounded-4xl overflow-hidden shadow-[0_0_50px_rgba(0,0,0,0.5)] border border-border animate-bounce-in">
        <div className="absolute top-0 left-0 w-full h-px bg-gradient-to-r from-transparent via-primary to-transparent opacity-50" />

        <div className="p-6 border-b border-border flex items-center gap-4">
          <div className="relative">
            {isSearching ? (
              <div className="w-6 h-6 border-2 border-primary/20 border-t-indigo-500 rounded-full animate-spin" />
            ) : (
              <MagnifyingGlassIcon className="w-6 h-6 text-primary" />
            )}
          </div>
          <input
            autoFocus
            type="text"
            placeholder="Type to search objects, run actions, or navigate..."
            className="bg-transparent border-none focus:ring-0 text-foreground placeholder-muted-foreground w-full text-xl font-medium outline-none"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="flex items-center gap-2">
            <span className="px-2 py-1 rounded-lg bg-foreground/5 border border-border text-xs font-bold text-muted-foreground uppercase tracking-wider">
              ESC
            </span>
          </div>
        </div>

        <div
          className="max-h-[60vh] overflow-y-auto p-3 scrollbar-hide"
          ref={scrollContainerRef}
        >
          {/*
            Shown whenever a source fell over, NOT only when the result list is
            empty. Three planes answering and one failing still produces a
            partial answer presented as a complete one — the same lie as the
            empty case, just harder to notice, because results on screen read
            as "the search worked".
          */}
          {failedSources.length > 0 && (
            <div
              role="status"
              className="mb-2 rounded-lg border border-warning/20 bg-warning/10 px-3 py-2 text-xs text-warning"
            >
              Incomplete — {failedSources.join(", ")} did not respond.
            </div>
          )}
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
                    ? "bg-primary/10 border-primary/30 shadow-lg"
                    : "bg-transparent border-transparent hover:bg-foreground/[0.02]"
                }`}
                onMouseEnter={() => setSelectedIndex(index)}
              >
                {index === selectedIndex && (
                  <div className="absolute left-0 w-1 h-6 bg-primary rounded-r-full" />
                )}
                <div className="flex items-center gap-4 text-left">
                  <div
                    className={`w-12 h-12 rounded-xl flex items-center justify-center transition-colors ${
                      index === selectedIndex
                        ? "bg-primary/20 text-primary"
                        : "bg-foreground/5 text-muted-foreground group-hover:bg-foreground/10"
                    }`}
                  >
                    <result.icon className="w-6 h-6" />
                  </div>
                  <div className="min-w-0">
                    <div
                      className={`text-sm font-bold truncate ${
                        index === selectedIndex
                          ? "text-foreground"
                          : "text-foreground"
                      }`}
                    >
                      {result.title}
                    </div>
                    {result.subtitle && (
                      <div className="text-xs text-muted-foreground truncate mt-0.5 font-medium uppercase tracking-wider">
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
                          className="px-2 py-1 rounded bg-foreground/5 border border-border text-xs font-mono text-muted-foreground"
                        >
                          {s}
                        </span>
                      ))}
                    </div>
                  )}
                  {index === selectedIndex && (
                    <ArrowRightIcon className="w-4 h-4 text-primary animate-slide-right" />
                  )}
                </div>
              </button>
            ))
          ) : query !== "" && !isSearching ? (
            <div className="py-20 text-center space-y-4">
              <div className="w-16 h-16 rounded-3xl bg-foreground/5 flex items-center justify-center mx-auto border border-dashed border-border">
                <MagnifyingGlassIcon className="w-8 h-8 text-muted-foreground" />
              </div>
              <div>
                {/*
                  "No matches" is only true when every source answered. With
                  one of them down the honest statement is that the answer is
                  incomplete — otherwise the palette tells an operator their
                  bucket does not exist while the plane that holds it is
                  simply unreachable.
                */}
                <p className="text-sm font-bold text-muted-foreground">
                  {failedSources.length > 0
                    ? "No matches among the sources that answered"
                    : `No matches found for \u201c${query}\u201d`}
                </p>
                <p className="text-xs text-muted-foreground uppercase tracking-wider mt-1">
                  {failedSources.length > 0
                    ? "The banner above names which"
                    : "Try searching for something else"}
                </p>
              </div>
            </div>
          ) : null}
        </div>

        <div className="p-4 bg-foreground/[0.02] border-t border-border flex items-center justify-between text-xs font-bold text-muted-foreground uppercase tracking-wider">
          <div className="flex gap-6">
            <span className="flex items-center gap-2">
              <kbd className="px-1.5 py-0.5 rounded bg-foreground/5 border border-border text-muted-foreground">
                ↑↓
              </kbd>
              Navigate
            </span>
            <span className="flex items-center gap-2">
              <kbd className="px-1.5 py-0.5 rounded bg-foreground/5 border border-border text-muted-foreground">
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
            <span className="text-primary/40">
              {scopedTenantSlug ? (
                <>
                  Scope:{" "}
                  <span className="text-primary/70 font-mono normal-case tracking-normal">
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
