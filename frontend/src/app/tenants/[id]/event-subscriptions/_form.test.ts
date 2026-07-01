import { describe, expect, it } from "vitest";

import type { EventSubscription } from "@/gen/paladin/admin/v1/types_pb";
import { EMPTY_FORM, buildSink, formFromSubscription } from "./_form";

// HTTP sink payload format (HttpSink.format) round-trip — the UI selector
// (Raw JSON / CloudEvents 1.0) exposed what was previously API-only.

function httpSub(format: string): EventSubscription {
  return {
    $typeName: "paladin.admin.v1.EventSubscription",
    name: "tenants/t-1/eventSubscriptions/s1",
    tenantId: "t-1",
    filter: "",
    disabled: false,
    resourceVersion: "v1",
    sink: {
      $typeName: "paladin.admin.v1.EventSink",
      target: {
        case: "http",
        value: {
          $typeName: "paladin.admin.v1.HttpSink",
          url: "https://hook.example.com",
          signingSecretRef: "",
          maxAttempts: 5,
          format,
        },
      },
    },
  } as EventSubscription;
}

const httpTarget = (sink: ReturnType<typeof buildSink>) =>
  sink.target.case === "http" ? sink.target.value : undefined;

describe("HTTP sink payload format", () => {
  it("defaults to raw ('') when the operator doesn't choose CloudEvents", () => {
    const sink = buildSink({ ...EMPTY_FORM, sinkType: "http", httpFormat: "" });
    expect(httpTarget(sink)?.format).toBe("");
  });

  it("builds the CloudEvents envelope format when selected", () => {
    const sink = buildSink({
      ...EMPTY_FORM,
      sinkType: "http",
      httpFormat: "cloudevents",
    });
    expect(httpTarget(sink)?.format).toBe("cloudevents");
  });

  it("never emits an unknown format string (guards against stray values)", () => {
    const sink = buildSink({
      ...EMPTY_FORM,
      sinkType: "http",
      httpFormat: "garbage",
    });
    // Only "" or "cloudevents" are valid; anything else falls back to raw.
    expect(httpTarget(sink)?.format).toBe("");
  });

  it("hydrates httpFormat from an existing subscription (edit round-trip)", () => {
    expect(formFromSubscription(httpSub("cloudevents")).httpFormat).toBe(
      "cloudevents",
    );
    expect(formFromSubscription(httpSub("")).httpFormat).toBe("");
  });
});
