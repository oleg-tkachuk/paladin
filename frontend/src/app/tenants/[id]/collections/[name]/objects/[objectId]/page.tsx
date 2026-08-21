// Object detail under a tenant-scoped Collection. The route param
// `objectId` here is the URL-encoded storage key (matches the
// backend's resource layout); the parent Collection comes from the
// `[name]` segment higher up the tree, so we don't need a query
// param to disambiguate (legacy /objects/<id>?collection=… form is
// gone — Q4 hard cut).

import { ObjectDetailView } from "@/components/features/ObjectDetailView";

interface ObjectDetailPageProps {
  params: Promise<{ id: string; name: string; objectId: string }>;
}

export default async function ObjectDetailPage({
  params,
}: ObjectDetailPageProps) {
  const { name, objectId } = await params;
  const storageKey = decodeURIComponent(objectId);
  const parentCollection = decodeURIComponent(name);

  return (
    <ObjectDetailView
      collection={storageKey}
      parentCollection={parentCollection}
    />
  );
}
