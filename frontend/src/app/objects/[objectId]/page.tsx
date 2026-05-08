import { DEFAULT_OBJECT_KEY } from "@/constants";
import { ObjectDetailView } from "@/components/features/ObjectDetailView";

interface ObjectDetailPageProps {
  params: Promise<{ objectId: string }>;
  searchParams: Promise<{ objectKey?: string }>;
}

export default async function ObjectDetailPage({
  params,
  searchParams,
}: ObjectDetailPageProps) {
  const { objectId } = await params;
  const { objectKey: parentObjectKey } = await searchParams;

  // The route param contains the URL-encoded storage key (not the UUID objectId).
  // Legacy links using objectId will still work if the backend resolves them,
  // but the canonical form uses the storage key.
  const storageKey = decodeURIComponent(objectId);

  return (
    <ObjectDetailView
      objectKey={storageKey}
      parentObjectKey={parentObjectKey || DEFAULT_OBJECT_KEY}
    />
  );
}
