// The console's short explanations of its own terms, each with the document
// that decides it. One module, so a term reads the same wherever it is
// explained and a test can hold every link to a file that exists.

/** Where the repository's documents are read on the web. */
export const DOCS_BASE_URL =
  "https://github.com/oleg-tkachuk/paladin/blob/main/docs/";

export interface HelpEntry {
  /** The term, as the page names it. */
  term: string;
  /** Two or three sentences: what it is and what it means for the reader. */
  text: string;
  /** The document that decides it, relative to docs/. */
  doc: string;
  /** What the link says. */
  docLabel: string;
}

export const HELP = {
  publicRead: {
    term: "Public read",
    text: "Anyone with an object's URL reads it, unsigned, from a public bucket — no Paladin call per read, and the URL does not expire. A public collection has no trash: a delete is permanent.",
    doc: "adr/0027-public-collections.md",
    docLabel: "ADR-0027",
  },
  objectLock: {
    term: "Object Lock",
    text: "Retention keeps an object from being deleted or overwritten until a date. COMPLIANCE cannot be shortened by anyone; GOVERNANCE can, with the bypass. A legal hold keeps the object until it is lifted.",
    doc: "adr/0013-object-lock-retention-and-legal-hold.md",
    docLabel: "ADR-0013",
  },
  bucketLayout: {
    term: "Storage layout",
    text: "Shared: the tenant's collections live in a bucket another tenant owns and granted it. Dedicated: the tenant owns a bucket of its own, provisioned on a backend.",
    doc: "adr/0015-per-tenant-bucket-layout.md",
    docLabel: "ADR-0015",
  },
  bucketGrants: {
    term: "Bucket grants",
    text: "Every bucket has a tenant owner. Another tenant may bind collections to it only once the owner grants it the bucket, and a revoke stops new bindings.",
    doc: "adr/0032-every-bucket-has-a-tenant-owner.md",
    docLabel: "ADR-0032",
  },
  capability: {
    term: "Capability",
    text: "A short-lived, revocable token issued for one tenant. It can sign its bearer in as that tenant, and only for the operations, resources and budget it names.",
    doc: "adr/0010-capability-as-establishing-credential.md",
    docLabel: "ADR-0010",
  },
  tenantTrash: {
    term: "Tenant trash",
    text: "A deleted tenant waits here and can be restored until it is purged. Its retention only marks it past retention; nothing purges a tenant on its own.",
    doc: "deletion-semantics.md",
    docLabel: "Deletion semantics",
  },
} as const satisfies Record<string, HelpEntry>;

export type HelpTopic = keyof typeof HELP;

/** Where a topic's document is read. */
export function helpHref(topic: HelpTopic): string {
  return DOCS_BASE_URL + HELP[topic].doc;
}
