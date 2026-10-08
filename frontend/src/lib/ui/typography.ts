// Typography pattern — single source of truth for the Paladin admin UI.
//
// Distilled from the /health page rebalance (commit "fix(health):
// correct worker Service port + larger fonts + expandable errors").
// The previous codebase mixed text-xs / text-tiny / text-micro in
// the same row, which read as cramped and inconsistent. This module
// pins the small set of text styles that show up across every page so
// pages can compose without re-deriving the same Tailwind soup.
//
// Usage:
//
//   import { T } from "@/lib/ui/typography";
//   <div className={T.label}>Status</div>
//   <div className={T.value}>{count}</div>
//
// Or:
//
//   import { typography } from "@/lib/ui/typography";
//   <span className={typography.code}>{commit}</span>
//
// Both names export the same object — pick whichever reads cleaner at
// the call site.
//
// The Tailwind classes are deliberately concrete (text-base, not a
// custom token) — Tailwind's purger needs literal strings to keep the
// classes in the final bundle. If you template-build a class string,
// the class won't ship.

export const typography = {
  // ─── Labels & captions ─────────────────────────────────────────────
  // Section headers, metadata labels ("Status", "Commit", "Last sync").
  // Always above a value, always uppercase, always muted.
  label: "text-xs uppercase tracking-wider text-muted-foreground font-medium",

  // The label's chip form (inside a Badge): the badge's own text size,
  // without the medium weight.
  labelTight: "text-sm uppercase tracking-wider text-muted-foreground",

  // ─── Values ────────────────────────────────────────────────────────
  // Headline values shown under a label — counts, numbers, version
  // strings, identifiers. Mono so digits align in tables and tabular
  // displays.
  value: "font-mono text-base tabular-nums",

  // Dense value variant for inline counts ("12/24 healthy"). One step
  // smaller than `value` — use when the value sits inline with body
  // text rather than as a standalone metric.
  valueInline: "font-mono text-sm tabular-nums",

  // ─── Code, IDs, paths ──────────────────────────────────────────────
  // Stable identifiers (UUIDs, names, SHA prefixes, paths). Mono so
  // they're visually distinct from prose.
  code: "font-mono text-sm",

  // Compact code — for a row's secondary identifier (parent ID,
  // hash short form). One step smaller than `code`.
  codeSmall: "font-mono text-xs",

  // ─── Body & helpers ────────────────────────────────────────────────
  // Default body text in cards / tables / forms. Matches Tailwind's
  // text-sm. Use this rather than text-xs for paragraph copy — the
  // smaller size felt cramped against text-base values.
  body: "text-sm",

  // Helper / hint text below an input, secondary descriptions.
  helper: "text-sm text-muted-foreground",

  // Smallest readable text. Use sparingly — latency badges, footnotes.
  // Don't use for anything an operator must read at a glance.
  hint: "text-xs text-muted-foreground",

  // ─── Status pills ──────────────────────────────────────────────────
  // Inline status indicator with a colour dot. Pair with the chart
  // tokens (text-chart-2 / text-chart-3 / text-destructive) at the
  // call site since the colour depends on the status enum.
  pill: "inline-flex items-center gap-1.5 text-sm font-medium",

  // The dot inside a pill. Slightly larger than the size-1.5 the page
  // used previously — readable at a glance without dominating the row.
  pillDot: "size-2 rounded-full",

  // ─── Card titles ───────────────────────────────────────────────────
  // CardTitle inside a card with role/identifier as the title. Mono
  // because these are usually keys ("api", "postgres", "primary") that
  // need to read as identifiers rather than display text.
  cardTitleCode: "font-mono text-base",

  // CardTitle for prose titles ("Recent activity", "Permissions").
  cardTitleProse: "text-base font-semibold",
} as const;

// Short alias for terser call sites.
export const T = typography;
