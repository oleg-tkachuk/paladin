/**
 * google.protobuf.Any needs a type registry to survive a JSON round trip.
 *
 * The type travels as a URL, so a JSON codec with no registry cannot turn it
 * back into a message — and the failure is not scoped to the field. The whole
 * response fails to decode, so one Any in one operation takes down the entire
 * ListOperations call.
 *
 * That is exactly what happened: PlatformOperationService returns
 * operations.metadata as an Any, the server packs the executor's JSON payload
 * as a Struct, and the console's transport carried no registry. The
 * background-operations drawer and the dashboard widget worked only while no
 * operation existed; the first one to appear broke both with
 * "google.protobuf.Struct is not in the type registry".
 */
import { describe, it, expect } from "vitest";
import { fromJson, toJson, createRegistry } from "@bufbuild/protobuf";
import { StructSchema } from "@bufbuild/protobuf/wkt";
import { ListOperationsResponseSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { anyRegistry } from "./any-registry";

/** A ListOperations response shaped exactly as the admin plane returns it —
 *  captured from the cluster, not invented. */
const wireResponse = {
  operations: [
    {
      name: "operations/01a02e67-64c5-748c-9061-02710e299e39",
      type: "BatchUpdateTags",
      metadata: {
        "@type": "type.googleapis.com/google.protobuf.Struct",
        value: {
          Collection: "mp-probe",
          ObjectIDs: ["01a02462-2e3d-7364-aa18-93a5d4089a6b"],
          Replace: false,
          Tags: { env: "staging" },
          TenantID: "01a02416-f9f1-7c9c-90f1-b3d04aa1ea71",
        },
      },
      initiatorTenantId: "01a02416-f9f1-7c9c-90f1-b3d04aa1ea71",
    },
  ],
};

describe("Any decoding", () => {
  it("fails without a registry — the reason the transport carries one", () => {
    // Pinning the failure keeps the fix honest: if a future @bufbuild release
    // starts resolving well-known types implicitly, this test says so rather
    // than leaving a registry nobody can justify.
    expect(() => fromJson(ListOperationsResponseSchema, wireResponse)).toThrow(
      /not in the type registry/,
    );
  });

  it("decodes with the transport's registry", () => {
    const msg = fromJson(ListOperationsResponseSchema, wireResponse, {
      registry: anyRegistry,
    });
    expect(msg.operations).toHaveLength(1);
    expect(msg.operations[0].name).toBe(
      "operations/01a02e67-64c5-748c-9061-02710e299e39",
    );
    // The payload has to survive, not merely not-throw: a decode that dropped
    // the Any would leave a client believing the operation carried no metadata.
    expect(msg.operations[0].metadata).toBeDefined();
    expect(msg.operations[0].metadata?.typeUrl).toBe(
      "type.googleapis.com/google.protobuf.Struct",
    );
  });

  it("round-trips back to the same wire shape", () => {
    const msg = fromJson(ListOperationsResponseSchema, wireResponse, {
      registry: anyRegistry,
    });
    const back = toJson(ListOperationsResponseSchema, msg, {
      registry: anyRegistry,
    }) as typeof wireResponse;

    expect(back.operations[0].metadata).toEqual(
      wireResponse.operations[0].metadata,
    );
  });

  it("registers Struct, which is what the server packs", () => {
    // connectshim's jsonToAny decodes each executor payload into a Struct and
    // packs that. If the server ever packs something else, this is the line
    // that has to change with it.
    expect(anyRegistry.getMessage("google.protobuf.Struct")).toBeDefined();
    expect(
      createRegistry(StructSchema).getMessage("google.protobuf.Struct"),
    ).toBeDefined();
  });
});
