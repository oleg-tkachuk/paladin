// The create actions pages offer the command palette (usePageAction), one id
// each, so a page and its test name the same entry.
export const CREATE_ACTION = {
  tenant: "create.tenant",
  bucket: "create.bucket",
  tenantBucket: "create.tenant-bucket",
  collection: "create.collection",
  tenantCollection: "create.tenant-collection",
  user: "create.user",
  storageBackend: "create.storage-backend",
  m2mToken: "create.m2m-token",
  capability: "create.capability",
} as const;
