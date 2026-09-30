import { StorageKind } from "@/gen/paladin/admin/v1/types_pb";

export const STORAGE_KIND_LABELS: Record<number, string> = {
  [StorageKind.AWS_S3]: "AWS S3",
  [StorageKind.S3_COMPATIBLE]: "S3-compatible",
  [StorageKind.GCS]: "GCS",
  [StorageKind.UNSPECIFIED]: "—",
};

/** The kinds a backend can be registered as. */
export const REGISTRABLE_STORAGE_KINDS: readonly StorageKind[] = [
  StorageKind.AWS_S3,
  StorageKind.S3_COMPATIBLE,
  StorageKind.GCS,
];
