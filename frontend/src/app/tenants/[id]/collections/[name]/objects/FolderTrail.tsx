"use client";

// The way back from the folder on screen to the collection's root, one button
// per folder. The last step is where the list is, and is not a button.
import { ChevronRightIcon, FolderIcon } from "@heroicons/react/24/outline";

import { folderTrail } from "@/lib/objectFolders";

export function FolderTrail({
  collection,
  prefix,
  onOpen,
}: {
  collection: string;
  prefix: string;
  onOpen: (prefix: string) => void;
}) {
  const trail = folderTrail(prefix);
  return (
    <nav
      aria-label="Folder"
      className="flex flex-wrap items-center gap-1 text-sm"
    >
      <FolderIcon className="size-4 text-muted-foreground" />
      {trail.map((crumb, i) => {
        const label = crumb.name || collection;
        const here = i === trail.length - 1;
        return (
          <span key={crumb.prefix} className="flex items-center gap-1">
            {i > 0 ? (
              <ChevronRightIcon className="size-3 text-muted-foreground" />
            ) : null}
            {here ? (
              <span aria-current="page" className="font-medium">
                {label}
              </span>
            ) : (
              <button
                type="button"
                onClick={() => onOpen(crumb.prefix)}
                className="text-muted-foreground hover:text-foreground hover:underline"
              >
                {label}
              </button>
            )}
          </span>
        );
      })}
    </nav>
  );
}
