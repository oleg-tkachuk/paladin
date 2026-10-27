import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import { formatTimestamp } from "@/lib/format/timestamp";
import {
  writerTenantId,
  CLEARED_LABEL,
  DRAFT_LABEL,
  DRAFT_SIDE,
  WRITER_NOT_RECORDED,
  defaultComparison,
  isCleared,
  layerTenantId,
  resolveComparison,
  sideLabel,
  sideText,
  versionLabel,
  versionName,
} from "./policyHistory";

const AT = timestampFromMs(1_800_000_000_000);
const WRITER = "7f9c2b1e-3d4a-4c5b-8e6f-0a1b2c3d4e5f";

function v(version: number, cedarPolicy = `text ${version}`) {
  return { version, cedarPolicy };
}

describe("versionLabel", () => {
  it.each([
    [
      "a version a user wrote",
      { version: 3, cedarPolicy: "permit", writtenBy: WRITER, writtenAt: AT },
      `v3 · ${WRITER} · ${formatTimestamp(AT)}`,
    ],
    [
      "an agent's version",
      { version: 2, cedarPolicy: "p", writtenBy: "agent:ci", writtenAt: AT },
      `v2 · agent:ci · ${formatTimestamp(AT)}`,
    ],
    [
      "a version no request wrote",
      {
        version: 1,
        cedarPolicy: "permit",
        writtenBy: undefined,
        writtenAt: AT,
      },
      `v1 · ${WRITER_NOT_RECORDED} · ${formatTimestamp(AT)}`,
    ],
    [
      "a version that cleared the policy",
      { version: 4, cedarPolicy: "", writtenBy: WRITER, writtenAt: AT },
      `v4 · ${WRITER} · ${formatTimestamp(AT)} · ${CLEARED_LABEL}`,
    ],
    [
      "a version with no time",
      { version: 5, cedarPolicy: "p", writtenBy: WRITER, writtenAt: undefined },
      `v5 · ${WRITER} · ${formatTimestamp(undefined)}`,
    ],
  ])("%s", (_, version, want) => {
    expect(versionLabel(version)).toBe(want);
  });
});

describe("isCleared", () => {
  it.each([
    ["", true],
    ["permit (principal, action, resource);", false],
    [" ", false],
  ])("%j → %s", (text, want) => {
    expect(isCleared({ cedarPolicy: text })).toBe(want);
  });
});

describe("defaultComparison", () => {
  it.each([
    [
      "two versions: the one before against the newest",
      [v(3), v(2), v(1)],
      false,
      { before: "v2", after: "v3" },
    ],
    [
      "two versions and a draft: still the versions",
      [v(3), v(2)],
      true,
      { before: "v2", after: "v3" },
    ],
    [
      "one version and a draft: the version against the draft",
      [v(1)],
      true,
      { before: "v1", after: DRAFT_SIDE },
    ],
    ["one version, no draft: nothing", [v(1)], false, null],
    ["no versions: nothing", [], true, null],
  ])("%s", (_, versions, hasDraft, want) => {
    expect(defaultComparison(versions, hasDraft)).toEqual(want);
  });
});

describe("resolveComparison", () => {
  const versions = [v(3), v(2), v(1, "")];
  it.each([
    [
      "nothing picked: the default",
      {},
      undefined,
      { before: "v2", after: "v3" },
    ],
    [
      "both picked",
      { before: "v1", after: "v2" },
      undefined,
      { before: "v1", after: "v2" },
    ],
    [
      "one picked: the other side the default's",
      { before: "v1" },
      undefined,
      { before: "v1", after: "v3" },
    ],
    [
      "the draft picked, while there is one",
      { after: DRAFT_SIDE },
      "draft",
      { before: "v2", after: DRAFT_SIDE },
    ],
    [
      "the draft picked, gone since",
      { after: DRAFT_SIDE },
      undefined,
      { before: "v2", after: "v3" },
    ],
    [
      "a version no longer loaded",
      { before: "v9" },
      undefined,
      { before: "v2", after: "v3" },
    ],
  ])("%s", (_, picked, draft, want) => {
    expect(resolveComparison(picked, versions, draft)).toEqual(want);
  });

  it("compares nothing when there is nothing to compare", () => {
    expect(resolveComparison({ before: "v1" }, [v(1)], undefined)).toBeNull();
  });
});

describe("sideText and sideLabel", () => {
  const versions = [v(2), v(1, "")];
  it.each([
    ["a version", "v2", "draft text", "text 2", "v2"],
    ["a cleared version holds no text", "v1", undefined, "", "v1"],
    ["the draft", DRAFT_SIDE, "draft text", "draft text", DRAFT_LABEL],
    [
      "the draft, when there is none",
      DRAFT_SIDE,
      undefined,
      undefined,
      DRAFT_LABEL,
    ],
    ["a version not loaded", "v7", undefined, undefined, "v7"],
  ])("%s", (_, side, draft, text, label) => {
    expect(sideText(side, versions, draft)).toBe(text);
    expect(sideLabel(side)).toBe(label);
  });
});

describe("versionName", () => {
  it("never reads as the draft", () => {
    expect(versionName(1)).toBe("v1");
    expect(versionName(1)).not.toBe(DRAFT_SIDE);
  });
});

describe("layerTenantId", () => {
  it.each([
    ["tenants/acme", "acme"],
    ["tenants/acme/collections/docs", "acme"],
    ["storageBackends/minio", ""],
    ["storageBackends/minio/buckets/data", ""],
    ["", ""],
  ])("%s → %j", (name, want) => {
    expect(layerTenantId(name)).toBe(want);
  });
});

describe("writerTenantId", () => {
  const LAYER = "11111111-1111-4111-8111-111111111111";
  const PLATFORM = "22222222-2222-4222-8222-222222222222";
  it.each([
    [
      "the writer's own tenant, when recorded",
      { writtenByTenantId: PLATFORM },
      PLATFORM,
    ],
    ["the layer's, when not", {}, LAYER],
  ])("%s", (_, version, want) => {
    expect(writerTenantId(version, LAYER)).toBe(want);
  });
});
