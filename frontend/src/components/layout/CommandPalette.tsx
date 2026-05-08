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
  TrashIcon,
  HeartIcon,
  Cog6ToothIcon,
  CommandLineIcon,
  ArrowRightIcon,
  ServerStackIcon,
  TagIcon,
  BuildingOfficeIcon,
} from "@heroicons/react/24/outline";
import { useActions } from "@/context/ActionsContext";
import { useScope } from "@/context/ScopeContext";
import { objectClient, tenantClient } from "@/lib/connect/client";
import { Object$ } from "@/gen/paladin/data/v1/types_pb";
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
  const { objectKey: scopedObjectKey } = useScope();
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  const staticNavs: SearchResult[] = useMemo(
    () => [
      {
        id: "nav-objects",
        type: "nav",
        title: "Object Explorer",
        subtitle: "Browse all stored objects",
        icon: DocumentIcon,
        shortcut: "G O",
        onSelect: () => router.push("/objects"),
      },
      {
        id: "nav-objectKeys",
        type: "nav",
        title: "Buckets Management",
        subtitle: "Manage storage objectKeys and configurations",
        icon: ServerStackIcon,
        shortcut: "G B",
        onSelect: () => router.push("/object-keys"),
      },
      {
        id: "nav-upload",
        type: "nav",
        title: "Upload Assets",
        subtitle: "Upload new files to object tags",
        icon: DocumentIcon,
        shortcut: "G U",
        onSelect: () => router.push("/upload"),
      },
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
        id: "nav-trash",
        type: "nav",
        title: "Trash Bin",
        subtitle: "Recover or delete trashed items",
        icon: TrashIcon,
        shortcut: "G T",
        onSelect: () => router.push("/trash"),
      },
      {
        id: "nav-object-tags",
        type: "nav",
        title: "Object Tags",
        subtitle: "Manage object classification and taxonomies",
        icon: TagIcon,
        shortcut: "G K",
        onSelect: () => router.push("/object-tags"),
      },
      {
        id: "nav-tenants",
        type: "nav",
        title: "Tenants",
        subtitle: "Manage tenant identity and metadata",
        icon: BuildingOfficeIcon,
        shortcut: "G L",
        onSelect: () => router.push("/tenants"),
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

      const navResults: SearchResult[] = staticNavs.filter(
        (n) =>
          n.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
          (n.subtitle &&
            n.subtitle.toLowerCase().includes(searchQuery.toLowerCase())),
      );

      setSearchResults([...actionResults, ...navResults]);
      setSelectedIndex(0);

      try {
        // Object/tenant search parked during proto migration. ListObjects /
        // ListTenants request shapes diverged (no top-level objectKey/pageSize);
        // palette will be wired against the new pagination model after auth +
        // multi-plane plumbing.
        void scopedObjectKey;
        const objectResults: SearchResult[] = [];
        const objectTagResults: SearchResult[] = [];
        const tenantResponse: {
          tenants: Array<{ tenantId: string; displayName: string }>;
        } = {
          tenants: [],
        };

        const tenantResults: SearchResult[] = tenantResponse.tenants.map(
          (t) => ({
            id: `tenant-${t.tenantId}`,
            type: "nav",
            title: t.displayName || t.tenantId,
            subtitle: `Tenant ID: ${t.tenantId}`,
            icon: BuildingOfficeIcon,
            onSelect: () => router.push(`/tenants?search=${t.tenantId}`),
          }),
        );

        if (
          objectResults.length > 0 ||
          objectTagResults.length > 0 ||
          tenantResults.length > 0
        ) {
          setSearchResults((prev) => [
            ...prev,
            ...objectTagResults,
            ...tenantResults,
            ...objectResults,
          ]);
        }
      } catch (err) {
        console.error("Command Palette API search failed", err);
        // We don't clear the local results if API fails
      } finally {
        setIsSearching(false);
      }
    },
    [actions, staticNavs, router, scopedObjectKey],
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
            (i) => (i + 1) % (searchResults.length || staticNavs.length),
          );
        } else if (e.key === "ArrowUp") {
          e.preventDefault();
          setSelectedIndex(
            (i) =>
              (i - 1 + (searchResults.length || staticNavs.length)) %
              (searchResults.length || staticNavs.length),
          );
        } else if (e.key === "Enter") {
          e.preventDefault();
          const results = searchResults.length > 0 ? searchResults : staticNavs;
          const selected = results[selectedIndex];
          if (selected) {
            selected.onSelect();
            setIsOpen(false);
          }
        }
        return;
      }

      // Global Shortcuts (g + ...)
      if (
        !isOpen &&
        !["INPUT", "TEXTAREA"].includes((e.target as HTMLElement).tagName)
      ) {
        const now = Date.now();
        const key = e.key.toLowerCase();

        if (lastKey === "g" && now - lastKeyTime < 500) {
          const nav = staticNavs.find((n) =>
            n.shortcut?.toLowerCase().endsWith(key),
          );
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
  }, [toggle, staticNavs, isOpen, searchResults, selectedIndex]);

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
    searchResults.length > 0 || query !== "" ? searchResults : staticNavs;

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
