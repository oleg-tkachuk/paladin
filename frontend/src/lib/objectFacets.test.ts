import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { FacetCountSchema } from "@/gen/paladin/common/v1/facet_pb";
import { ObjectContentCategory } from "@/gen/paladin/data/v1/object_service_pb";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { parseFacets } from "@/lib/table/facets";
import { PAGE_TABLE } from "@/lib/table/urlState";

import {
  OBJECT_FACETS,
  OBJECT_FACET_STATUS,
  OBJECT_FACET_TYPE,
  objectFacetCounts,
  objectFacetsRequest,
  viewFacetSelection,
} from "./objectFacets";

/** One count as ListObjectsResponse.facet_counts carries it. */
const count = (facet: string, value: string, n: bigint) =>
  create(FacetCountSchema, { facet, value, count: n });

const options = (id: string) =>
  OBJECT_FACETS.find((f) => f.id === id)?.options?.map((o) => o.value);

describe("OBJECT_FACETS", () => {
  it("offers the states and categories the enums name, in their order", () => {
    // DELETED is the trash's: it lists deleted objects on a page of its own.
    expect(options(OBJECT_FACET_STATUS)).toEqual(["PENDING", "AVAILABLE"]);
    expect(options(OBJECT_FACET_TYPE)).toEqual([
      "image",
      "video",
      "audio",
      "text",
      "pdf",
      "json",
      "other",
    ]);
  });
});

// The links the single-choice filters wrote must open the same choice.
describe("the URL the page wrote before the facets", () => {
  it.each([
    ["status=AVAILABLE", { status: ["AVAILABLE"], type: [] }],
    ["type=pdf", { status: [], type: ["pdf"] }],
    // The listing no longer offers DELETED: the trash lists those objects.
    ["status=DELETED&type=image", { status: [], type: ["image"] }],
    [
      "status=AVAILABLE,PENDING",
      { status: ["PENDING", "AVAILABLE"], type: [] },
    ],
    // A value no facet offers reads as no choice, never as a filter.
    ["status=active", { status: [], type: [] }],
    [
      "type=" + encodeURIComponent('image" || true || "'),
      { status: [], type: [] },
    ],
  ])("reads %s", (query, want) => {
    expect(
      parseFacets(new URLSearchParams(query), OBJECT_FACETS, PAGE_TABLE),
    ).toEqual(want);
  });
});

describe("viewFacetSelection", () => {
  it.each([
    ["nothing", {}, { status: [], type: [] }],
    [
      "an older view's one choice each",
      { status: "AVAILABLE", type: "pdf" },
      { status: ["AVAILABLE"], type: ["pdf"] },
    ],
    [
      "several choices",
      { status: ["AVAILABLE", "PENDING"], type: ["other", "image"] },
      { status: ["PENDING", "AVAILABLE"], type: ["image", "other"] },
    ],
    [
      "values the facets do not offer",
      { status: "pending", type: ["image", "docs"] },
      { status: [], type: ["image"] },
    ],
  ])("reads %s", (_, filters, want) => {
    expect(viewFacetSelection(filters)).toEqual(want);
  });
});

describe("objectFacetsRequest", () => {
  it.each([
    ["no choice", {}, { states: [], contentCategories: [] }],
    [
      "several of each",
      { status: ["AVAILABLE", "PENDING"], type: ["pdf", "other"] },
      {
        states: [ObjectState.AVAILABLE, ObjectState.PENDING],
        contentCategories: [
          ObjectContentCategory.PDF,
          ObjectContentCategory.OTHER,
        ],
      },
    ],
    [
      "values no enum names, nor the unspecified",
      { status: ["UNSPECIFIED", "FAILED"], type: ["unspecified", "docs"] },
      { states: [], contentCategories: [] },
    ],
  ])("sends %s", (_, selection, want) => {
    expect(objectFacetsRequest(selection)).toEqual(want);
  });
});

describe("objectFacetCounts", () => {
  it("names the server's facets and answers as the page's facets do", () => {
    const counts = objectFacetCounts([
      count("state", "OBJECT_STATE_AVAILABLE", 12n),
      count("state", "OBJECT_STATE_DELETED", 3n),
      count("content_category", "OBJECT_CONTENT_CATEGORY_PDF", 4n),
      count("content_category", "OBJECT_CONTENT_CATEGORY_OTHER", 1n),
    ]);
    expect(counts.get(OBJECT_FACET_STATUS)).toEqual(
      new Map([
        ["AVAILABLE", 12],
        ["DELETED", 3],
      ]),
    );
    expect(counts.get(OBJECT_FACET_TYPE)).toEqual(
      new Map([
        ["pdf", 4],
        ["other", 1],
      ]),
    );
  });

  it("leaves out a facet or an answer the page does not offer", () => {
    const counts = objectFacetCounts([
      count("owner", "ops", 2n),
      count("state", "OBJECT_STATE_GONE", 2n),
    ]);
    expect(counts.size).toBe(0);
  });
});
