import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import { OperationSchema } from "@/gen/paladin/admin/v1/operation_service_pb";
import { AuditLogEntrySchema } from "@/gen/paladin/admin/v1/types_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

import {
  actorKind,
  auditFilterFor,
  operationsFilterFor,
  historyFromAudit,
  historyFromOperation,
  mergeHistory,
  plainAction,
} from "./history";

const USER = "0b9d3c62-1f7c-4c4e-9a53-3f2a2b8d2a11";
const at = (iso: string) => timestampFromDate(new Date(iso));

describe("actorKind", () => {
  // Argo CD tells a person's sync from an automated one; a token is how
  // automation acts here.
  it.each([
    [USER, "user"],
    ["apikey:7c2a", "token"],
    ["capability:9f1e", "token"],
    ["system:bootstrap", "system"],
    ["", "system"],
  ])("%s → %s", (subject, kind) => {
    expect(actorKind(subject)).toBe(kind);
  });
});

describe("plainAction", () => {
  it("says an RPC's method and an event's last step in words", () => {
    expect(plainAction("/paladin.admin.v1.BucketService/UpdateBucket")).toBe(
      "Update bucket",
    );
    expect(plainAction("iam.bootstrap_admin.password_reset")).toBe(
      "Password reset",
    );
  });
});

describe("historyFromAudit", () => {
  it("reads a failure as a fault, with the server's reason and the record", () => {
    const e = historyFromAudit(
      create(AuditLogEntrySchema, {
        entryId: "e-1",
        at: at("2026-10-20T10:00:00Z"),
        action: "/paladin.admin.v1.TenantService/UpdateTenant",
        actorSubject: USER,
        actorTenantId: USER,
        errorMessage: "permission_denied: not yours",
      }),
    );
    expect(e).toMatchObject({
      tone: "danger",
      outcome: "Failed",
      action: "Update tenant",
      error: "not yours",
      durationMs: null,
      href: "/audit?id=e-1",
      actor: { kind: "user" },
    });
  });
});

describe("historyFromOperation", () => {
  const now = new Date("2026-10-20T10:05:00Z");

  it("reads a running batch as a change under way, timed so far", () => {
    const e = historyFromOperation(
      create(OperationSchema, {
        name: "operations/op-1",
        type: "BatchDelete",
        state: OperationState.RUNNING,
        createdAt: at("2026-10-20T10:00:00Z"),
        initiatorSubject: "apikey:7c2a",
      }),
      now,
    );
    expect(e).toMatchObject({
      tone: "progress",
      outcome: "Running",
      action: "Move to trash",
      durationMs: 5 * 60_000,
      href: "/operations?id=operations%2Fop-1",
      actor: { kind: "token" },
    });
  });

  it("times a finished one from start to finish", () => {
    const e = historyFromOperation(
      create(OperationSchema, {
        name: "operations/op-2",
        type: "BatchCopy",
        state: OperationState.SUCCEEDED,
        done: true,
        createdAt: at("2026-10-20T10:00:00Z"),
        doneAt: at("2026-10-20T10:00:12Z"),
      }),
      now,
    );
    expect(e).toMatchObject({ tone: "success", durationMs: 12_000 });
  });
});

describe("mergeHistory", () => {
  it("is one timeline, newest first, an undated entry last", () => {
    const audit = historyFromAudit(
      create(AuditLogEntrySchema, {
        entryId: "a",
        at: at("2026-10-20T09:00:00Z"),
      }),
    );
    const undated = historyFromAudit(
      create(AuditLogEntrySchema, { entryId: "b" }),
    );
    const op = historyFromOperation(
      create(OperationSchema, {
        name: "operations/c",
        createdAt: at("2026-10-20T10:00:00Z"),
      }),
      new Date(),
    );
    expect(mergeHistory([undated, audit], [op]).map((e) => e.key)).toEqual([
      "operation:operations/c",
      "audit:a",
      "audit:b",
    ]);
  });
});

describe("filters", () => {
  // The slash keeps bucket "logs" from matching "logs-old".
  it("names the resource and what it holds", () => {
    expect(auditFilterFor("storageBackends/b/buckets/logs")).toBe(
      'resource_name == "storageBackends/b/buckets/logs" || resource_name.startsWith("storageBackends/b/buckets/logs/")',
    );
    expect(auditFilterFor("storageBackends/b", false)).toBe(
      'resource_name == "storageBackends/b"',
    );
    expect(operationsFilterFor("t-1")).toBe('tenant_id == "t-1"');
  });
});
