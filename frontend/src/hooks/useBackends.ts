"use client";

import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";

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
import { useBumpRefresh } from "@/context/RefreshContext";
import { errorMessage } from "@/hooks/errorContract";
import { fieldMask, type MaskField } from "@/lib/connect/fieldMask";
import { useOptimistic } from "@/hooks/useOptimistic";
import { edit, type OptimisticEdit } from "@/lib/optimistic";
import {
  BACKENDS_QUERY_KEY,
  backendResourceName,
  backendsQuery,
} from "@/lib/queries/backends";
import { invalidatePolicyVersions } from "@/lib/queries/policyVersions";

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
const UPDATE_BACKEND_MASK: MaskField<typeof StorageBackendSchema>[] = [
  "displayName",
  "endpoint",
  "publicEndpoint",
  "region",
  "forcePathStyle",
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
function updateMaskFor(
  input: UpdateBackendInput,
): MaskField<typeof StorageBackendSchema>[] {
  const paths = [...UPDATE_BACKEND_MASK];
  if (input.sse !== undefined) paths.push("sse");
  if (input.events !== undefined) paths.push("events");
  if (input.cedarPolicy !== undefined) paths.push("cedarPolicy");
  return paths;
}

/** How a page reads the backends. */
export interface UseBackendsOptions {
  /** CEL over the backends (searchFilter() from @/lib/cel); "" for none. */
  filter?: string;
  /** False holds the read: a dialog until it opens, a component that only changes them. */
  enabled?: boolean;
}

/** A backend's fields as a change leaves them, in every list that holds it. */
function changed(backendId: string, fields: Partial<StorageBackend>) {
  return edit<StorageBackend[]>(BACKENDS_QUERY_KEY, (list) =>
    list.map((b) => (b.backendId === backendId ? { ...b, ...fields } : b)),
  );
}

/**
 * The storage backends, read through the query cache (lib/queries/backends),
 * and the changes to them. A change the console can predict — a switch, an
 * edit of the plain fields, a delete — shows in every cached list at once and
 * is put back if the server refuses it (lib/optimistic).
 */
export function useBackends({
  filter = "",
  enabled = true,
}: UseBackendsOptions = {}) {
  const bumpRefresh = useBumpRefresh();
  const qc = useQueryClient();
  const optimistic = useOptimistic();
  const query = useQuery({
    ...backendsQuery(filter),
    enabled,
    // A new search keeps the last result on screen until it answers.
    placeholderData: (previous) => previous,
  });
  const backends = useMemo(() => query.data ?? [], [query.data]);
  const error = query.error
    ? errorMessage(query.error, "Failed to fetch backends")
    : null;
  const { refetch: refetchQuery } = query;
  const refetch = useCallback(() => void refetchQuery(), [refetchQuery]);

  // Every backends list reads again after a change: the server's answer may
  // differ from the prediction (a health check, a bumped version).
  const settle = useCallback(() => {
    void qc.invalidateQueries({ queryKey: BACKENDS_QUERY_KEY });
    bumpRefresh("backends");
  }, [qc, bumpRefresh]);

  /** Runs a change, shown at once by `edits`, then reads every list again. */
  const run = useCallback(
    async <T>(edits: OptimisticEdit[], fn: () => Promise<T>): Promise<T> => {
      try {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        return await optimistic(edits, fn);
      } finally {
        settle();
      }
    },
    [optimistic, settle],
  );

  // createBackend — wraps BackendService.CreateBackend. The proto's
  // Create takes the backend_id at the top level and a nested
  // StorageBackend resource carrying the rest of the fields. Tenants
  // and buckets cannot exist without a registered backend, so this
  // is the entry point for first-run platform bootstrap and any
  // later expansion to a new region/cluster.
  const createBackend = useCallback(
    async (input: CreateBackendInput): Promise<StorageBackend> => {
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
      // The server answers with its health and features: nothing to predict.
      return run([], () =>
        backendClient.createBackend({ backendId: input.backendId, backend }),
      );
    },
    [run],
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
      return run([changed(backendId, { enabled })], () =>
        backendClient.setBackendEnabled({
          name: `storageBackends/${backendId}`,
          enabled,
          resourceVersion,
        }),
      );
    },
    [run],
  );

  // setBackendReadOnly — flips a backend's read-only "drain" state via
  // BackendService.SetBackendReadOnly (`001_initial_schema.sql`). OCC-guarded on
  // resourceVersion. Draining keeps reads working while refusing mutations,
  // so an operator can migrate data off before disabling. Unlike disable,
  // the default backend may be drained.
  const setBackendReadOnly = useCallback(
    async (
      backendId: string,
      readOnly: boolean,
      resourceVersion: string,
    ): Promise<StorageBackend> => {
      return run([changed(backendId, { readOnly })], () =>
        backendClient.setBackendReadOnly({
          name: `storageBackends/${backendId}`,
          readOnly,
          resourceVersion,
        }),
      );
    },
    [run],
  );

  // setBackendMaintenance — raises/clears the operator-set maintenance flag
  // via BackendService.SetBackendMaintenance (`001_initial_schema.sql`). OCC-guarded.
  // Advisory only — it does not gate operations.
  const setBackendMaintenance = useCallback(
    async (
      backendId: string,
      maintenance: boolean,
      resourceVersion: string,
    ): Promise<StorageBackend> => {
      return run([changed(backendId, { maintenance })], () =>
        backendClient.setBackendMaintenance({
          name: `storageBackends/${backendId}`,
          maintenance,
          resourceVersion,
        }),
      );
    },
    [run],
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
      // The plain fields are predicted; encryption, events and the policy
      // show as the server stores them, once it answers.
      const plain = {
        displayName: input.displayName,
        endpoint: input.endpoint,
        publicEndpoint: input.publicEndpoint,
        region: input.region,
        forcePathStyle: input.forcePathStyle,
      };
      const name = backendResourceName(backendId);
      const updated = await run([changed(backendId, plain)], () =>
        backendClient.updateBackend({
          name,
          resourceVersion,
          updateMask: fieldMask(StorageBackendSchema, ...updateMaskFor(input)),
          backend,
        }),
      );
      // A policy written is the layer's newest version.
      if (input.cedarPolicy !== undefined) {
        void invalidatePolicyVersions(qc, name);
      }
      return updated;
    },
    [run, qc],
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
      return run(
        [changed(backendId, { credentialsSecretRef: newSecretRef })],
        () =>
          backendClient.rotateCredentials({
            name: `storageBackends/${backendId}`,
            newSecretRef,
            gracePeriod,
          }),
      );
    },
    [run],
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
      await run(
        [
          edit<StorageBackend[]>(BACKENDS_QUERY_KEY, (list) =>
            list.filter((b) => b.backendId !== backendId),
          ),
        ],
        () =>
          backendClient.deleteBackend({
            name: `storageBackends/${backendId}`,
            resourceVersion,
          }),
      );
    },
    [run],
  );

  return {
    backends,
    loading: query.isFetching,
    error,
    refetch,
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
