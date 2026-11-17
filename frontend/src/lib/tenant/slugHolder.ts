// What holds a tenant slug a create was refused, read off the SLUG_TAKEN
// error, and where in the console the operator can do something about it.
import { ErrorReason } from "@/gen/paladin/common/v1/error_reason_pb";
import { errorMetadata, errorReason } from "@/hooks/errorContract";
import { SECTION } from "@/lib/navigation/sections";

/**
 * The SLUG_TAKEN ErrorInfo metadata key naming the holder, and its values:
 * the contract on ERROR_REASON_SLUG_TAKEN in common/v1/error_reason.proto.
 */
export const SLUG_HOLDER_KEY = "holder";

export const SlugHolder = {
  /** A tenant using the slug. */
  Live: "live",
  /** A tenant in the trash, which may still be restored. */
  Trashed: "trashed",
  /** A purged tenant; the slug is kept until it is released. */
  Retired: "retired",
} as const;
export type SlugHolder = (typeof SlugHolder)[keyof typeof SlugHolder];

/** The id of the retired-slugs section on the trash page. */
export const RETIRED_SLUGS_ANCHOR = "retired-slugs";

/** Where the retired slugs are listed and released. */
export const RETIRED_SLUGS_HREF = `${SECTION.trash}#${RETIRED_SLUGS_ANCHOR}`;

const HOLDERS: readonly string[] = Object.values(SlugHolder);

/**
 * What holds the slug err refused, null when err is no SLUG_TAKEN or names
 * no holder this console knows — a server older or newer than it.
 */
export function slugTakenBy(err: unknown): SlugHolder | null {
  if (errorReason(err) !== ErrorReason.SLUG_TAKEN) return null;
  const holder = errorMetadata(err)[SLUG_HOLDER_KEY];
  return holder && HOLDERS.includes(holder) ? (holder as SlugHolder) : null;
}

/** What to tell the operator about a taken slug, and where to go about it. */
export interface SlugTakenHint {
  text: string;
  href?: string;
  linkLabel?: string;
}

export function slugTakenHint(holder: SlugHolder): SlugTakenHint {
  switch (holder) {
    case SlugHolder.Live:
      return { text: "Another tenant uses this slug. Pick another." };
    case SlugHolder.Trashed:
      return {
        text: "A tenant in the trash holds this slug. Restore it, or purge it and release the slug.",
        href: SECTION.trash,
        linkLabel: "Open the Trash",
      };
    case SlugHolder.Retired:
      return {
        text: "A purged tenant held this slug, so it is reserved. Release it to reuse it.",
        href: RETIRED_SLUGS_HREF,
        linkLabel: "Retired slugs",
      };
  }
}
