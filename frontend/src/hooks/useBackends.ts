"use client";

import { useCallback, useEffect, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { DurationSchema, FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { ConnectError } from "@connectrpc/connect";

import { backendClient } from "@/lib/connect/client";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import {
  EventSourceConfigSchema,
  EventTarget,
  ServerSideEncryptionSchema,
  SseType,
  StorageBackendSchema,
  StorageKind,
} from "@/gen/paladin/admin/v1/types_pb";
import type { TestBackendResponse } from "@/gen/paladin/admin/v1/backend_service_pb";
import { useBumpRefresh, useRefreshSignal } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

// Ceiling on how many pages one fetch will follow — a backstop against paging
// forever if a token ever fails to terminate, not a limit anyone should reach.
const MAX_LIST_PAGES = 20;

// useBackends — list rows from the `backends` table via
// admin/v1.BackendService.ListBackends. Use this for any UI that needs
// the set of bind-targets (bucket creation, Collection provisioning, etc.).
// Do NOT parse `storage.backends` from yaml config: bucket FK points at
// this table, so config-only entries can't actually receive Buckets and
// will trip `buckets_backend_id_fkey` on insert.

export interface CreateBackendInput {
  backendId: string;
  displayName: string;
  kind: StorageKind;
  endpoint: string;
  publicEndpoint?: string;
  region: string;
  forcePathStyle: boolean;
  credentialsSecretRef: string;
}

// UpdateBackendInput — the operator-editable fields. Credentials are rotated
// via rotateCredentials (own RPC + grace window), and enable/drain/maintenance
// flags have dedicated Set* RPCs, so none of those belong here.
//
// The three advanced fields are OPTIONAL, and that is the whole contract:
// omitted means "leave it alone", present means "write this". See the mask
// note below for why they cannot simply always be sent.
export interface UpdateBackendInput {
  displayName: string;
  endpoint: string;
  publicEndpoint: string;
  region: string;
  forcePathStyle: boolean;
  // Server-side encryption applied at presign time. keyId is required by the
  // server when type is KMS.
  sse?: { type: SseType; keyId: string };
  // Storage-event source. pollIntervalMs is carried as a number here and
  // converted to a protobuf Duration on the way out.
  events?: {
    enabled: boolean;
    target: EventTarget;
    queueUrl: string;
    pollIntervalMs: number;
  };
  // Cedar policy attached to the backend itself.
  cedarPolicy?: string;
}

// The flat metadata fields are always sent with a matching mask. The dialog
// pre-fills current values, so re-sending an unchanged one is a harmless
// same-value write, and a fixed mask keeps that request deterministic.
const UPDATE_BACKEND_MASK = [
  "display_name",
  "endpoint",
  "public_endpoint",
  "region",
  "force_path_style",
];

// The advanced fields are NOT in that list, and adding them there would be a
// bug rather than a simplification.
//
// `sse`, `events` and `cedar_policy` are mask-gated as whole groups on the
// server (adapters/admin_backend.go `Update`): naming "events" in the mask
// writes enabled, target, queue_url AND poll_interval from the message, in
// one statement. The flat fields survive a round trip through the form
// unchanged because they are strings and a bool. A group does not: an enum
// value this build of the console does not know collapses to UNSPECIFIED, and
// an absent Duration reads back as zero. So a fixed mask would let "rename the
// backend" silently clear a KMS key id or an SQS queue URL — a write nobody
// asked for, on fields whose whole job is to be set once and left alone.
//
// Hence: a group is named in the mask only when the caller supplies it, and
// the dialog supplies it only when its section was actually edited.
function updateMaskFor(input: UpdateBackendInput): string[] {
  const paths = [...UPDATE_BACKEND_MASK];
  if (input.sse !== undefined) paths.push("sse");
  if (input.events !== undefined) paths.push("events");
  if (input.cedarPolicy !== undefined) paths.push("cedar_policy");
  return paths;
}

export function useBackends(autoFetch: boolean = true) {
  const [backends, setBackends] = useState<StorageBackend[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const bumpRefresh = useBumpRefresh();

  const fetchBackends = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // Follows nextPageToken to the end. Every caller here asks for "the
      // backends" — the scope picker, the create-collection and create-bucket
      // dialogs, the tables — and a single page silently hid the rest, exactly
      // as it did for buckets until that bit: a backend past the ceiling was
      // invisible and unselectable everywhere, with nothing on screen saying
      // the list was cut. There are few backends today, which is the only
      // reason this had not surfaced yet.
      let token = "";
      let pages = 0;
      const acc: StorageBackend[] = [];
      do {
        const res = await backendClient.listBackends({
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token },
          filter: "",
        });
        acc.push(...res.backends);
        token = res.page?.nextPageToken ?? "";
        pages += 1;
      } while (token && pages < MAX_LIST_PAGES);
      setBackends(acc);
      return acc;
    } catch (err) {
      // Query contract (state-only): surface via `error`, never throw.
      setError(
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to fetch backends",
      );
      return [];
    } finally {
      setLoading(false);
    }
  }, []);

  // createBackend — wraps BackendService.CreateBackend. The proto's
  // Create takes the backend_id at the top level and a nested
  // StorageBackend resource carrying the rest of the fields. Tenants
  // and buckets cannot exist without a registered backend, so this
  // is the entry point for first-run platform bootstrap and any
  // later expansion to a new region/cluster.
  const createBackend = useCallback(
    async (input: CreateBackendInput): Promise<StorageBackend> => {
      try {
        setError(null);
        const backend = create(StorageBackendSchema, {
          backendId: input.backendId,
          displayName: input.displayName,
          kind: input.kind,
          endpoint: input.endpoint,
          publicEndpoint: input.publicEndpoint || "",
          region: input.region,
          forcePathStyle: input.forcePathStyle,
          credentialsSecretRef: input.credentialsSecretRef,
        });
        const created = await backendClient.createBackend({
          backendId: input.backendId,
          backend,
        });
        setBackends((prev) => [...prev, created]);
        bumpRefresh("backends");
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // setBackendEnabled — flips a backend's enable/disable state via
  // BackendService.SetBackendEnabled. OCC-guarded: pass the backend's
  // current resourceVersion. The server refuses to disable the configured
  // default backend (FailedPrecondition) and rejects a stale version
  // (Aborted) — callers should surface err.rawMessage to the operator.
  const setBackendEnabled = useCallback(
    async (
      backendId: string,
      enabled: boolean,
      resourceVersion: string,
    ): Promise<StorageBackend> => {
      try {
        setError(null);
        const updated = await backendClient.setBackendEnabled({
          name: `storageBackends/${backendId}`,
          enabled,
          resourceVersion,
        });
        setBackends((prev) =>
          prev.map((b) => (b.backendId === backendId ? updated : b)),
        );
        bumpRefresh("backends");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // setBackendReadOnly — flips a backend's read-only "drain" state via
  // BackendService.SetBackendReadOnly (migration 047). OCC-guarded on
  // resourceVersion. Draining keeps reads working while refusing mutations,
  // so an operator can migrate data off before disabling. Unlike disable,
  // the default backend may be drained.
  const setBackendReadOnly = useCallback(
    async (
      backendId: string,
      readOnly: boolean,
      resourceVersion: string,
    ): Promise<StorageBackend> => {
      try {
        setError(null);
        const updated = await backendClient.setBackendReadOnly({
          name: `storageBackends/${backendId}`,
          readOnly,
          resourceVersion,
        });
        setBackends((prev) =>
          prev.map((b) => (b.backendId === backendId ? updated : b)),
        );
        bumpRefresh("backends");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // setBackendMaintenance — raises/clears the operator-set maintenance flag
  // via BackendService.SetBackendMaintenance (migration 049). OCC-guarded.
  // Advisory only — it does not gate operations.
  const setBackendMaintenance = useCallback(
    async (
      backendId: string,
      maintenance: boolean,
      resourceVersion: string,
    ): Promise<StorageBackend> => {
      try {
        setError(null);
        const updated = await backendClient.setBackendMaintenance({
          name: `storageBackends/${backendId}`,
          maintenance,
          resourceVersion,
        });
        setBackends((prev) =>
          prev.map((b) => (b.backendId === backendId ? updated : b)),
        );
        bumpRefresh("backends");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // updateBackend — edits mutable metadata via BackendService.UpdateBackend,
  // OCC-guarded on resourceVersion. Returns the server's fresh copy so callers
  // pick up the bumped resource_version for a subsequent edit.
  const updateBackend = useCallback(
    async (
      backendId: string,
      resourceVersion: string,
      input: UpdateBackendInput,
    ): Promise<StorageBackend> => {
      try {
        setError(null);
        const backend = create(StorageBackendSchema, {
          backendId,
          displayName: input.displayName,
          endpoint: input.endpoint,
          publicEndpoint: input.publicEndpoint,
          region: input.region,
          forcePathStyle: input.forcePathStyle,
          // Only built when supplied — an unset sub-message paired with an
          // unset mask path is what "leave it alone" looks like on the wire.
          ...(input.sse !== undefined && {
            sse: create(ServerSideEncryptionSchema, {
              type: input.sse.type,
              keyId: input.sse.keyId,
            }),
          }),
          ...(input.events !== undefined && {
            events: create(EventSourceConfigSchema, {
              enabled: input.events.enabled,
              target: input.events.target,
              queueUrl: input.events.queueUrl,
              // Duration is seconds + nanos; the form works in whole
              // milliseconds, so split rather than truncate to seconds — a
              // 500ms poll interval must not round to 0.
              pollInterval: create(DurationSchema, {
                seconds: BigInt(Math.floor(input.events.pollIntervalMs / 1000)),
                nanos: (input.events.pollIntervalMs % 1000) * 1_000_000,
              }),
            }),
          }),
          ...(input.cedarPolicy !== undefined && {
            cedarPolicy: input.cedarPolicy,
          }),
        });
        const updated = await backendClient.updateBackend({
          name: `storageBackends/${backendId}`,
          resourceVersion,
          updateMask: create(FieldMaskSchema, { paths: updateMaskFor(input) }),
          backend,
        });
        setBackends((prev) =>
          prev.map((b) => (b.backendId === backendId ? updated : b)),
        );
        bumpRefresh("backends");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // rotateCredentials — points the backend at a new secret-store ref via
  // BackendService.RotateCredentials. The old secret stays cached until
  // `gracePeriod` elapses so in-flight ops don't break mid-rotation. Empty
  // gracePeriod lets the server apply its default.
  const rotateCredentials = useCallback(
    async (
      backendId: string,
      newSecretRef: string,
      gracePeriod: string,
    ): Promise<StorageBackend> => {
      try {
        setError(null);
        const updated = await backendClient.rotateCredentials({
          name: `storageBackends/${backendId}`,
          newSecretRef,
          gracePeriod,
        });
        setBackends((prev) =>
          prev.map((b) => (b.backendId === backendId ? updated : b)),
        );
        bumpRefresh("backends");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // testBackend — probes connectivity via BackendService.TestBackend. This is
  // a read-only diagnostic (no state change), so it follows the query contract
  // and returns the probe result for the caller to display. It does NOT set
  // `error` — a failed probe (reachable=false) is a successful RPC.
  const testBackend = useCallback(
    async (backendId: string): Promise<TestBackendResponse> => {
      return await backendClient.testBackend({
        name: `storageBackends/${backendId}`,
      });
    },
    [],
  );

  // deleteBackend — removes a backend via BackendService.DeleteBackend,
  // OCC-guarded. The server refuses (FailedPrecondition) while buckets still
  // reference it, and nothing overrides that: buckets.backend_id is
  // ON DELETE RESTRICT. The `force` argument this used to take promised
  // otherwise. Caller redirects away on success.
  const deleteBackend = useCallback(
    async (backendId: string, resourceVersion: string): Promise<void> => {
      try {
        setError(null);
        await backendClient.deleteBackend({
          name: `storageBackends/${backendId}`,
          resourceVersion,
        });
        setBackends((prev) => prev.filter((b) => b.backendId !== backendId));
        bumpRefresh("backends");
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  const refreshSignal = useRefreshSignal("backends");
  useEffect(() => {
    // Fetch-on-mount / on-refresh: this is a deliberate sync with an
    // external system (the BackendService list), not derived state.
    // fetchBackends toggles loading internally; the set-state-in-effect
    // rule is a false positive for this data-loading pattern.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (autoFetch) void fetchBackends();
  }, [autoFetch, fetchBackends, refreshSignal]);

  return {
    backends,
    loading,
    error,
    fetchBackends,
    createBackend,
    updateBackend,
    rotateCredentials,
    testBackend,
    deleteBackend,
    setBackendEnabled,
    setBackendReadOnly,
    setBackendMaintenance,
  };
}
