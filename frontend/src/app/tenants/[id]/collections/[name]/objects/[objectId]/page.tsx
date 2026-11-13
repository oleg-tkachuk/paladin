"use client";

// Object detail under a tenant-scoped Collection. The route param
// `objectId` here is the URL-encoded storage key (matches the
// backend's resource layout); the parent Collection comes from the
// `[name]` segment higher up the tree, so we don't need a query
// param to disambiguate (legacy /objects/<id>?collection=… form is
// gone — Q4 hard cut).

import { ObjectDetailView } from "@/components/features/ObjectDetailView";
import { useRouteParams } from "@/lib/navigation/routeParams";

export default function ObjectDetailPage() {
  const { name, objectId } = useRouteParams(
    "/tenants/[id]/collections/[name]/objects/[objectId]",
  );
  return <ObjectDetailView collection={objectId} parentCollection={name} />;
}
