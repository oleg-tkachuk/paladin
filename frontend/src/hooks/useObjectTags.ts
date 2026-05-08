// Stub during migration. Old backend exposed ObjectTag as a standalone
// resource (list/create/update/delete). New backend has only per-object
// tag map (Get/Put/Delete on a specific object). The standalone ObjectTag
// surface (page, sidebar count, command palette, dashboard, upload picker)
// is parked until product decides whether to bring back a taxonomy service
// or move tagging entirely into the object detail view.

import { useState, useCallback } from "react";

export type ObjectTagCompat = {
  slug: string;
  displayName: string;
  description: string;
  labels: Record<string, string>;
};

const NOT_IMPLEMENTED = "Object tags taxonomy is parked — see migration notes";

export function useObjectTags() {
  const [objectTags] = useState<ObjectTagCompat[]>([]);
  const [loading] = useState(false);
  const [error] = useState<string | null>(null);

  const fetchObjectTags = useCallback(
    async (_pageSize: number = 50, _pageToken: string = "") => {
      return { objectTags: [] as ObjectTagCompat[], nextPageToken: "" };
    },
    [],
  );

  const createObjectTag = useCallback(
    async (
      _slug: string,
      _displayName: string = "",
      _description: string = "",
      _labels: Record<string, string> = {},
    ): Promise<ObjectTagCompat> => {
      throw new Error(NOT_IMPLEMENTED);
    },
    [],
  );

  const deleteObjectTag = useCallback(
    async (_slug: string, _resourceVersion: string = ""): Promise<void> => {
      throw new Error(NOT_IMPLEMENTED);
    },
    [],
  );

  const updateObjectTag = useCallback(
    async (
      _slug: string,
      _resourceVersion: string,
      _updatePaths: string[],
      _fields: {
        displayName?: string;
        description?: string;
        labels?: Record<string, string>;
      } = {},
    ): Promise<ObjectTagCompat> => {
      throw new Error(NOT_IMPLEMENTED);
    },
    [],
  );

  return {
    objectTags,
    loading,
    error,
    fetchObjectTags,
    createObjectTag,
    updateObjectTag,
    deleteObjectTag,
  };
}
