import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { FacetCountSchema } from "@/gen/paladin/common/v1/facet_pb";
import {
  OperationState,
  OperationStateSchema,
} from "@/gen/paladin/common/v1/operation_pb";
import { countedOptions } from "@/lib/table/facets";
import { StatusTone } from "@/lib/ui/status";

import {
  OPERATION_FACETS,
  OP_BATCH_COPY,
  OP_FACET_STATE,
  OP_FACET_TYPE,
  operationFacetCounts,
  operationFacetsRequest,
} from "./operations";

const [STATE, TYPE] = OPERATION_FACETS;
const LISTED = [
  OperationState.PENDING,
  OperationState.RUNNING,
  OperationState.SUCCEEDED,
  OperationState.FAILED,
  OperationState.CANCELLED,
];

// The server counts a state by its wire name and the request takes its
// number; a slip between them is a menu of zeros, so each state is carried
// all the way round.
describe("operation facets", () => {
  it.each(LISTED)(
    "state %s goes from a count to a choice to a request",
    (state) => {
      const wire = OperationStateSchema.value[state].name;
      const counts = operationFacetCounts([
        create(FacetCountSchema, {
          facet: OP_FACET_STATE,
          value: wire,
          count: 3n,
        }),
      ]);
      const option = countedOptions(
        STATE,
        counts.get(OP_FACET_STATE) ?? new Map(),
        [],
      ).find((o) => o.count === 3);
      expect(option).toBeDefined();
      const request = operationFacetsRequest({
        [OP_FACET_STATE]: [option!.value],
      });
      expect(request.states).toEqual([state]);
    },
  );

  it("offers every state an operation can be in, toned as its status", () => {
    expect(STATE.options?.map((o) => o.label)).toEqual([
      "Queued",
      "Running",
      "Succeeded",
      "Failed",
      "Cancelled",
    ]);
    expect(STATE.options?.find((o) => o.label === "Failed")?.tone).toBe(
      StatusTone.Danger,
    );
  });

  it("offers the types counted, in words, and asks for them by name", () => {
    const counts = operationFacetCounts([
      create(FacetCountSchema, {
        facet: OP_FACET_TYPE,
        value: OP_BATCH_COPY,
        count: 2n,
      }),
    ]);
    expect(
      countedOptions(TYPE, counts.get(OP_FACET_TYPE) ?? new Map(), []),
    ).toEqual([{ value: OP_BATCH_COPY, label: "Batch copy", count: 2 }]);
    expect(
      operationFacetsRequest({ [OP_FACET_TYPE]: [OP_BATCH_COPY] }),
    ).toEqual({
      states: [],
      types: [OP_BATCH_COPY],
    });
  });

  it("asks for nothing a choice does not name", () => {
    expect(
      operationFacetsRequest({ [OP_FACET_STATE]: ["NOT_A_STATE"] }),
    ).toEqual({
      states: [],
      types: [],
    });
  });
});
