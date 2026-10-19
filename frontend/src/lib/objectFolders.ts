// A collection's keys seen as folders: the level a prefix names, the trail
// back to the root, and each name as it shows at that level. Pure, so the
// page and its tests agree on where a key belongs.

/** What separates one folder from the next in a key. */
export const FOLDER_DELIMITER = "/";

/** One step of the trail from the collection's root to a folder. */
export interface FolderCrumb {
  /** The folder's own name; empty for the root. */
  name: string;
  /** The prefix that lists it. */
  prefix: string;
}

/**
 * The folder a URL names, as a prefix the list can use: empty for the root,
 * otherwise ending in the delimiter. Leading delimiters are dropped, since no
 * key starts at an empty folder.
 */
export function folderPrefix(raw: string | null | undefined): string {
  const trimmed = (raw ?? "").replace(/^\/+/, "");
  if (!trimmed) return "";
  return trimmed.endsWith(FOLDER_DELIMITER)
    ? trimmed
    : `${trimmed}${FOLDER_DELIMITER}`;
}

/** The trail from the root to `prefix`, root first. */
export function folderTrail(prefix: string): FolderCrumb[] {
  const trail: FolderCrumb[] = [{ name: "", prefix: "" }];
  let at = "";
  for (const name of prefix.split(FOLDER_DELIMITER).filter(Boolean)) {
    at += `${name}${FOLDER_DELIMITER}`;
    trail.push({ name, prefix: at });
  }
  return trail;
}

/** A folder's name at the level `prefix` lists: its segment, no delimiter. */
export function folderName(commonPrefix: string, prefix: string): string {
  return commonPrefix.slice(prefix.length).replace(/\/$/, "");
}
