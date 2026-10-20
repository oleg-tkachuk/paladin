// The console's keys in UserSettings.preferences, a free-form Struct the
// server stores as given. Each key's value has a schema here, read through
// it, so a value another client wrote — or an older console — reads as the
// default instead of breaking a page.
import { z } from "zod";

export const PREFERENCE_KEYS = {
  recent: "console.recent",
  pinned: "console.pinned",
  notifyOperations: "console.notifyOperations",
  hideResourceLinks: "console.hideResourceLinks",
  tables: "console.tables",
  diff: "console.diff",
  collapseStatusPanels: "console.collapseStatusPanels",
} as const;

export type PreferenceKey =
  (typeof PREFERENCE_KEYS)[keyof typeof PREFERENCE_KEYS];

/** A page of one resource, as the sidebar and the palette list it. */
export const ResourceLinkSchema = z.object({
  // A path in this console: "//host" would leave it, protocol-relative.
  href: z
    .string()
    .startsWith("/")
    .refine(
      (h) => !h.startsWith("//") && !h.includes("\\"),
      "not a local path",
    ),
  label: z.string(),
  /** What the resource is: "Collection", "Tenant". */
  kind: z.string(),
});
export type ResourceLink = z.infer<typeof ResourceLinkSchema>;

export const ResourceLinksSchema = z.array(ResourceLinkSchema);

/** An on/off preference: anything but true is off. */
export function readFlag(value: unknown): boolean {
  return value === true;
}
