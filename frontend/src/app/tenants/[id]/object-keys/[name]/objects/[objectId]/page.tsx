// Object detail under a tenant-scoped ObjectKey. The route param
// `objectId` here is the URL-encoded storage key (matches the
// backend's resource layout); the parent ObjectKey comes from the
// `[name]` segment higher up the tree, so we don't need a query
// param to disambiguate (legacy /objects/<id>?objectKey=… form is
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
  const parentObjectKey = decodeURIComponent(name);

  return (
    <ObjectDetailView
      objectKey={storageKey}
      parentObjectKey={parentObjectKey}
    />
  );
}
