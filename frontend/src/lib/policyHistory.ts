// What the policy history shows of a layer's versions, and which two of them
// it compares. The versions arrive newest first, as ListPolicyVersions pages
// them; the editor's unsaved text, when there is one, is the draft.
import type { PolicyVersion } from "@/gen/paladin/admin/v1/policy_service_pb";
import { formatTimestamp } from "@/lib/format/timestamp";

/** What a version that cleared the policy shows in place of its text. */
export const CLEARED_LABEL = "(cleared)";
/** What a version no request wrote — a migration, psql — is attributed to. */
export const WRITER_NOT_RECORDED = "not recorded";
/** What the editor's unsaved text is called beside the versions. */
export const DRAFT_LABEL = "Draft";
/**
 * The draft's place among the compared sides. A version's is its name, "v3",
 * which never reads so.
 */
export const DRAFT_SIDE = "draft";

/** One side of a comparison: a version's name, or the draft. */
export type ComparisonSide = string;

export interface Comparison {
  before: ComparisonSide;
  after: ComparisonSide;
}

/** What a layer's resource name begins with when a tenant owns the layer. */
const TENANT_LAYER = /^tenants\/([^/]+)/;

/** A version as the history names it: "v3". */
export function versionName(version: number): string {
  return `v${version}`;
}

/** The version cleared the policy: it holds no text. */
export function isCleared(v: Pick<PolicyVersion, "cedarPolicy">): boolean {
  return v.cedarPolicy === "";
}

/** A version in one line: "v3 · <writer> · <when>", and "(cleared)" if so. */
export function versionLabel(
  v: Pick<PolicyVersion, "version" | "cedarPolicy" | "writtenBy" | "writtenAt">,
): string {
  const parts = [
    versionName(v.version),
    v.writtenBy || WRITER_NOT_RECORDED,
    formatTimestamp(v.writtenAt),
  ];
  if (isCleared(v)) parts.push(CLEARED_LABEL);
  return parts.join(" · ");
}

/** What a side is called above the diff: "v3", or "Draft". */
export function sideLabel(side: ComparisonSide): string {
  return side === DRAFT_SIDE ? DRAFT_LABEL : side;
}

/**
 * The text a side holds — "" for a cleared version — or undefined when no
 * loaded version, and no draft, is that side.
 */
export function sideText(
  side: ComparisonSide,
  versions: readonly Pick<PolicyVersion, "version" | "cedarPolicy">[],
  draft: string | undefined,
): string | undefined {
  if (side === DRAFT_SIDE) return draft;
  return versions.find((v) => versionName(v.version) === side)?.cedarPolicy;
}

/**
 * What the history compares before the user picks: the newest version against
 * the one before it. A layer with one version compares it with the draft, and
 * one with none compares nothing.
 */
export function defaultComparison(
  versions: readonly Pick<PolicyVersion, "version">[],
  hasDraft: boolean,
): Comparison | null {
  const [newest, previous] = versions;
  if (newest && previous) {
    return {
      before: versionName(previous.version),
      after: versionName(newest.version),
    };
  }
  if (newest && hasDraft) {
    return { before: versionName(newest.version), after: DRAFT_SIDE };
  }
  return null;
}

/**
 * The comparison shown: each side the user picked, while it is still there to
 * compare, and the default's otherwise.
 */
export function resolveComparison(
  picked: Partial<Comparison>,
  versions: readonly Pick<PolicyVersion, "version" | "cedarPolicy">[],
  draft: string | undefined,
): Comparison | null {
  const fallback = defaultComparison(versions, draft !== undefined);
  const present = (side: ComparisonSide | undefined): side is string =>
    side !== undefined && sideText(side, versions, draft) !== undefined;
  const before = present(picked.before) ? picked.before : fallback?.before;
  const after = present(picked.after) ? picked.after : fallback?.after;
  return before !== undefined && after !== undefined ? { before, after } : null;
}

/**
 * The tenant a layer belongs to, which names its writers' user records: the
 * id in "tenants/{id}/…", and "" for a backend's or a bucket's layer.
 */
export function layerTenantId(resourceName: string): string {
  return TENANT_LAYER.exec(resourceName)?.[1] ?? "";
}
