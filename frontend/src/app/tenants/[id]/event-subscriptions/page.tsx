"use client";

import { useCallback, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  BoltIcon,
  EllipsisVerticalIcon,
  PencilSquareIcon,
  PlayIcon,
  PlusIcon,
  PowerIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";

import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dropdown } from "@/components/ui/Dropdown";
import { Skeleton } from "@/components/ui/Skeleton";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "../tenant-context";
import { eventSubscriptionClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { EventSubscription } from "@/gen/paladin/admin/v1/types_pb";
import { sinkSummary, truncate, type TestResult } from "./_form";
import { TestResultDisplay } from "./_components";
import { SubscriptionEditorDialog } from "./SubscriptionEditorDialog";

// ─── Page ─────────────────────────────────────────────────────────────
export default function EventsPage() {
  // tenantId comes from the URL (TenantLayout). Legacy /events
  // pulled it from useScope() so the page only listed subscriptions
  // for the signed-in tenant; the new path lets platform-admins
  // manage any tenant's subs by navigating in.
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  const [tests, setTests] = useState<Map<string, TestResult>>(new Map());
  const [pendingTest, setPendingTest] = useState<Set<string>>(new Set());

  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<EventSubscription | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<EventSubscription | null>(
    null,
  );
  const [deleting, setDeleting] = useState(false);

  const queryClient = useQueryClient();
  const esKey = ["eventSubscriptions", tenantId] as const;
  const listQuery = useQuery({
    queryKey: esKey,
    enabled: !!tenantId,
    retry: false, // queryFn toasts; a retry would double-toast.
    queryFn: async ({ signal }) => {
      try {
        const res = await eventSubscriptionClient.listSubscriptions(
          { parent: `tenants/${tenantId}` },
          { signal },
        );
        return res.subscriptions;
      } catch (err) {
        showNotification({
          type: "error",
          title: "Load failed",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to list event subscriptions",
        });
        throw err;
      }
    },
  });
  const items = listQuery.data ?? [];
  const loading = listQuery.isFetching;
  const hasFetched = listQuery.isFetched;
  const fetchList = () => listQuery.refetch();
  // Optimistic-update shim: writes straight into the query cache, so the
  // existing handlers (handleSaved / handleToggle / handleDelete) keep their
  // setItems(curr => …) and setItems(array) call shapes unchanged.
  const setItems = (
    next:
      | EventSubscription[]
      | ((curr: EventSubscription[]) => EventSubscription[]),
  ) =>
    queryClient.setQueryData<EventSubscription[]>(esKey, (curr) =>
      typeof next === "function" ? next(curr ?? []) : next,
    );

  // ── Open create / edit ────────────────────────────────────────────
  // The editor dialog owns the form + save RPC; the page only tracks which
  // subscription (if any) is being edited and whether the dialog is open.
  const openCreate = () => {
    setEditing(null);
    setEditorOpen(true);
  };

  const openEdit = (sub: EventSubscription) => {
    setEditing(sub);
    setEditorOpen(true);
  };

  const closeEditor = () => {
    setEditorOpen(false);
    setEditing(null);
  };

  // Merge a created/updated subscription returned by the editor into the list.
  const handleSaved = (result: EventSubscription, wasEdit: boolean) => {
    setItems((curr) =>
      wasEdit
        ? curr.map((s) => (s.name === result.name ? result : s))
        : [result, ...curr],
    );
  };

  // ── Test ─────────────────────────────────────────────────────────
  const handleTest = useCallback(
    async (sub: EventSubscription) => {
      setPendingTest((prev) => {
        const next = new Set(prev);
        next.add(sub.name);
        return next;
      });
      try {
        const res = await eventSubscriptionClient.testSubscription({
          name: sub.name,
        });
        const result: TestResult = {
          delivered: res.delivered,
          statusCode: res.statusCode,
          errorMessage: res.errorMessage,
          at: Date.now(),
        };
        setTests((curr) => {
          const next = new Map(curr);
          next.set(sub.name, result);
          return next;
        });
      } catch (err) {
        const msg =
          err instanceof ConnectError ? err.rawMessage : "Test failed";
        showNotification({ type: "error", title: "Test failed", message: msg });
      } finally {
        setPendingTest((prev) => {
          const next = new Set(prev);
          next.delete(sub.name);
          return next;
        });
      }
    },
    [showNotification],
  );

  // ── Toggle disabled (optimistic) ─────────────────────────────────
  const handleToggle = async (sub: EventSubscription) => {
    if (!tenantId) return;
    const target = !sub.disabled;
    const prevItems = items;
    setItems((curr) =>
      curr.map((s) => (s.name === sub.name ? { ...s, disabled: target } : s)),
    );
    try {
      const updated = await eventSubscriptionClient.updateSubscription({
        name: sub.name,
        resourceVersion: sub.resourceVersion,
        updateMask: create(FieldMaskSchema, { paths: ["disabled"] }),
        subscription: {
          $typeName: "paladin.admin.v1.EventSubscription",
          name: sub.name,
          tenantId,
          filter: sub.filter,
          sink: sub.sink,
          disabled: target,
          resourceVersion: sub.resourceVersion,
        },
      });
      setItems((curr) =>
        curr.map((s) => (s.name === updated.name ? updated : s)),
      );
    } catch (err) {
      setItems(prevItems);
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        showNotification({
          type: "warning",
          title: "Stale, please retry",
          message: "Subscription was modified concurrently — refetching.",
        });
        await fetchList();
        return;
      }
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Toggle failed";
      showNotification({ type: "error", title: "Toggle failed", message: msg });
    }
  };

  // ── Delete ────────────────────────────────────────────────────────
  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await eventSubscriptionClient.deleteSubscription({
        name: deleteTarget.name,
        resourceVersion: deleteTarget.resourceVersion,
      });
      setItems((curr) => curr.filter((s) => s.name !== deleteTarget.name));
      setTests((curr) => {
        const next = new Map(curr);
        next.delete(deleteTarget.name);
        return next;
      });
      showNotification({
        type: "success",
        title: "Subscription deleted",
        message: "Sink will no longer receive events.",
      });
      setDeleteTarget(null);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        showNotification({
          type: "warning",
          title: "Stale, please retry",
          message: "Subscription was modified concurrently — refetching.",
        });
        await fetchList();
        setDeleteTarget(null);
        return;
      }
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Delete failed";
      showNotification({ type: "error", title: "Delete failed", message: msg });
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Event subscriptions</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Outbound event sinks (HTTP webhook / Kafka / SQS) for{" "}
            <span className="font-mono">{tenant.displayName}</span>. Each sink
            is filtered by a CEL predicate over EventEnvelope.
          </p>
        </div>
        <Button onClick={openCreate} size="sm">
          <PlusIcon className="size-4" />
          New subscription
        </Button>
      </div>

      <Card variant="outline" className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Sink</TableHead>
              <TableHead className="hidden md:table-cell">Filter</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="hidden lg:table-cell">Last test</TableHead>
              <TableHead className="w-12 text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && !hasFetched ? (
              Array.from({ length: 3 }).map((_, i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={5}>
                    <Skeleton className="h-8 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : items.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5}>
                  <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
                    <div className="flex size-12 items-center justify-center rounded-2xl bg-muted">
                      <BoltIcon className="size-6 text-muted-foreground" />
                    </div>
                    <div className="max-w-md px-4">
                      <p className="text-sm font-medium">
                        No event subscriptions
                      </p>
                      <p className={cn(T.helper, "mt-1 text-balance")}>
                        Forward Paladin events to a webhook (HTTP) or NATS subject.
                        Kafka / SQS sinks are roadmap stubs.
                      </p>
                    </div>
                    <Button onClick={openCreate} size="sm">
                      <PlusIcon className="size-4" />
                      New subscription
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              items.map((sub) => {
                const summary = sinkSummary(sub);
                const last = tests.get(sub.name);
                const testing = pendingTest.has(sub.name);
                return (
                  <TableRow key={sub.name}>
                    <TableCell>
                      <div className="flex flex-col gap-0.5 min-w-0">
                        <div className="flex items-center gap-2">
                          <Badge variant="outline" className="text-[10px]">
                            {summary.badge}
                          </Badge>
                        </div>
                        <span
                          className={cn(T.codeSmall, "truncate max-w-[420px]")}
                          title={summary.detail}
                        >
                          {summary.detail}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      {sub.filter ? (
                        <span
                          className={cn(T.code, "block truncate max-w-[260px]")}
                          title={sub.filter}
                        >
                          {sub.filter}
                        </span>
                      ) : (
                        <span className={cn(T.helper, "italic")}>
                          all events
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      {sub.disabled ? (
                        <span
                          className={cn(T.pill, "text-muted-foreground")}
                          aria-label="disabled"
                        >
                          <span
                            className={cn(T.pillDot, "bg-muted-foreground/60")}
                          />
                          disabled
                        </span>
                      ) : (
                        <span
                          className={cn(T.pill, "text-chart-2")}
                          aria-label="active"
                        >
                          <span className={cn(T.pillDot, "bg-chart-2")} />
                          active
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="hidden lg:table-cell">
                      {testing ? (
                        <span className={cn(T.hint)}>Testing…</span>
                      ) : last ? (
                        <TestResultDisplay result={last} />
                      ) : (
                        <span className={cn(T.hint, "italic")}>—</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <Dropdown align="right" width="w-44">
                        <Dropdown.Trigger
                          className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          activeClassName="bg-accent text-foreground"
                        >
                          <span className="sr-only">
                            Actions for subscription {sub.name}
                          </span>
                          <EllipsisVerticalIcon className="size-4" />
                        </Dropdown.Trigger>
                        <Dropdown.Menu className="py-1">
                          <Dropdown.Item
                            className="p-0"
                            onClick={() => openEdit(sub)}
                          >
                            <div className="flex w-full items-center gap-2 px-3 py-1.5 text-xs hover:bg-accent">
                              <PencilSquareIcon className="size-4 text-muted-foreground" />
                              Edit
                            </div>
                          </Dropdown.Item>
                          <Dropdown.Item
                            className="p-0"
                            onClick={() => void handleTest(sub)}
                          >
                            <div className="flex w-full items-center gap-2 px-3 py-1.5 text-xs hover:bg-accent">
                              <PlayIcon className="size-4 text-muted-foreground" />
                              Test
                            </div>
                          </Dropdown.Item>
                          <Dropdown.Item
                            className="p-0"
                            onClick={() => void handleToggle(sub)}
                          >
                            <div className="flex w-full items-center gap-2 px-3 py-1.5 text-xs hover:bg-accent">
                              <PowerIcon className="size-4 text-muted-foreground" />
                              {sub.disabled ? "Enable" : "Disable"}
                            </div>
                          </Dropdown.Item>
                          <div className="my-1 h-px bg-border" />
                          <Dropdown.Item
                            className="p-0"
                            onClick={() => setDeleteTarget(sub)}
                          >
                            <div className="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10">
                              <TrashIcon className="size-4" />
                              Delete
                            </div>
                          </Dropdown.Item>
                        </Dropdown.Menu>
                      </Dropdown>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ─── Editor dialog ───────────────────────────────────────── */}
      <SubscriptionEditorDialog
        open={editorOpen}
        editing={editing}
        tenantId={tenantId}
        onClose={closeEditor}
        onSaved={handleSaved}
        onStale={() => void fetchList()}
        testResult={editing ? tests.get(editing.name) : undefined}
        testing={editing ? pendingTest.has(editing.name) : false}
        onTest={() => editing && void handleTest(editing)}
      />

      {/* ─── Delete confirm ─────────────────────────────────────── */}
      <ConfirmModal
        isOpen={deleteTarget !== null}
        onClose={() => (deleting ? undefined : setDeleteTarget(null))}
        onConfirm={handleDelete}
        type="danger"
        title="Delete subscription?"
        message={
          deleteTarget
            ? `Sink at ${truncate(sinkSummary(deleteTarget).detail, 80)} will stop receiving events. Existing in-flight deliveries continue.`
            : ""
        }
        confirmText="Delete"
        loading={deleting}
      />
    </div>
  );
}
