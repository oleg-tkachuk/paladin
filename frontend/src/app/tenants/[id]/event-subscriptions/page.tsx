"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
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
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useNotification } from "@/components/ui/Notification";
import { useTenant } from "../tenant-context";
import { eventSubscriptionClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { useCELValidation } from "@/hooks/useCELValidation";
import { CELIndicator } from "@/components/ui/CELIndicator";
import type { EventSubscription } from "@/gen/paladin/admin/v1/types_pb";
import {
  TEMPLATES,
  SINK_OPTIONS,
  EMPTY_FORM,
  formFromSubscription,
  buildSink,
  validateForm,
  sinkSummary,
  truncate,
  type TemplateId,
  type FormState,
  type FormErrors,
  type TestResult,
} from "./_form";
import {
  FormSection,
  Field,
  ToggleRow,
  TestResultDisplay,
} from "./_components";

// ─── Page ─────────────────────────────────────────────────────────────
export default function EventsPage() {
  // tenantId comes from the URL (TenantLayout). Legacy /events
  // pulled it from useScope() so the page only listed subscriptions
  // for the signed-in tenant; the new path lets platform-admins
  // manage any tenant's subs by navigating in.
  const tenant = useTenant();
  const tenantId = tenant.tenantId;
  const { showNotification } = useNotification();

  const [items, setItems] = useState<EventSubscription[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasFetched, setHasFetched] = useState(false);
  const [tests, setTests] = useState<Map<string, TestResult>>(new Map());
  const [pendingTest, setPendingTest] = useState<Set<string>>(new Set());

  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<EventSubscription | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [errors, setErrors] = useState<FormErrors>({});
  const [saving, setSaving] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<EventSubscription | null>(
    null,
  );
  const [deleting, setDeleting] = useState(false);

  const fetchList = useCallback(async () => {
    if (!tenantId) {
      setItems([]);
      setHasFetched(false);
      return;
    }
    setLoading(true);
    try {
      const res = await eventSubscriptionClient.listSubscriptions({
        parent: `tenants/${tenantId}`,
      });
      setItems(res.subscriptions);
      setHasFetched(true);
    } catch (err) {
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to list event subscriptions";
      showNotification({
        type: "error",
        title: "Load failed",
        message: msg,
      });
      setHasFetched(true);
    } finally {
      setLoading(false);
    }
  }, [tenantId, showNotification]);

  useEffect(() => {
    if (!tenantId) return;
    void fetchList();
  }, [tenantId, fetchList]);

  // ── Open create / edit ────────────────────────────────────────────
  const openCreate = () => {
    setEditing(null);
    setForm(EMPTY_FORM);
    setErrors({});
    setEditorOpen(true);
  };

  const openEdit = (sub: EventSubscription) => {
    setEditing(sub);
    setForm(formFromSubscription(sub));
    setErrors({});
    setEditorOpen(true);
  };

  const closeEditor = () => {
    setEditorOpen(false);
    setEditing(null);
    setErrors({});
  };

  const applyTemplate = (id: TemplateId) => {
    const tpl = TEMPLATES.find((t) => t.id === id);
    if (!tpl) return;
    setForm((prev) => ({
      ...prev,
      template: id,
      sinkType: tpl.forcesHttp ? "http" : prev.sinkType,
      filter: tpl.filter !== undefined ? tpl.filter : prev.filter,
    }));
  };

  // ── Save (create or update) ───────────────────────────────────────
  const handleSave = async () => {
    if (!tenantId) return;
    const e = validateForm(form);
    setErrors(e);
    if (Object.keys(e).length > 0) return;

    setSaving(true);
    const sink = buildSink(form);
    try {
      if (editing) {
        const updated = await eventSubscriptionClient.updateSubscription({
          name: editing.name,
          resourceVersion: editing.resourceVersion,
          updateMask: create(FieldMaskSchema, {
            paths: ["filter", "sink", "disabled"],
          }),
          subscription: {
            $typeName: "paladin.admin.v1.EventSubscription",
            name: editing.name,
            tenantId,
            filter: form.filter,
            sink,
            disabled: form.disabled,
            resourceVersion: editing.resourceVersion,
          },
        });
        setItems((curr) =>
          curr.map((s) => (s.name === updated.name ? updated : s)),
        );
        showNotification({
          type: "success",
          title: "Subscription updated",
          message: "Event sink configuration saved.",
        });
      } else {
        const created = await eventSubscriptionClient.createSubscription({
          parent: `tenants/${tenantId}`,
          subscription: {
            $typeName: "paladin.admin.v1.EventSubscription",
            name: "",
            tenantId,
            filter: form.filter,
            sink,
            disabled: form.disabled,
            resourceVersion: "",
          },
        });
        setItems((curr) => [created, ...curr]);
        showNotification({
          type: "success",
          title: "Subscription created",
          message: "Event sink is live.",
        });
      }
      closeEditor();
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        showNotification({
          type: "warning",
          title: "Stale, please retry",
          message: "Subscription was modified concurrently — refetching.",
        });
        await fetchList();
        return;
      }
      const msg = err instanceof ConnectError ? err.rawMessage : "Save failed";
      showNotification({ type: "error", title: "Save failed", message: msg });
    } finally {
      setSaving(false);
    }
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

  // ── Submitable in editor? Used by the inline Test button. ────────
  const editingTestable = useMemo(() => {
    if (!editing) return false;
    return Object.keys(validateForm(form)).length === 0;
  }, [editing, form]);

  // Live CEL validation against the EventEnvelope schema. Mirrors the
  // lifecycle editor: keep submit + Test disabled while in flight or on
  // a compile error so operators learn about bad filters at edit-time
  // rather than at first event delivery.
  const filterCELState = useCELValidation(form.filter, "EventEnvelope");
  const filterCELBlocksSubmit =
    filterCELState.status === "invalid" ||
    filterCELState.status === "validating";

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
                        Forward PALADIN events to a webhook (HTTP) or NATS subject.
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
      <Dialog
        open={editorOpen}
        onOpenChange={(o) => (o ? setEditorOpen(true) : closeEditor())}
      >
        <DialogContent className="max-w-[calc(100%-2rem)] sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>
              {editing ? "Edit subscription" : "New event subscription"}
            </DialogTitle>
            <DialogDescription>
              {editing
                ? "Update the sink configuration and CEL filter for this subscription."
                : "Forward events from this tenant to a webhook (HTTP) or NATS subject. Kafka / SQS sinks are roadmap stubs."}
            </DialogDescription>
          </DialogHeader>

          <div className="max-h-[70vh] overflow-y-auto space-y-5 py-2 pr-1">
            {!editing && (
              <FormSection title="Template">
                <Field
                  label="Connector"
                  hint="Pick a known connector to prefill the URL placeholder and a recommended CEL filter. Pick Custom for full manual control."
                >
                  <ToggleRow
                    options={TEMPLATES.map((t) => ({
                      id: t.id,
                      label: t.label,
                    }))}
                    selected={form.template}
                    onSelect={(id) => applyTemplate(id)}
                  />
                </Field>
              </FormSection>
            )}

            <FormSection title="Sink type">
              <Field
                label="Target"
                hint="HTTP delivers an HMAC-signed POST. NATS publishes a CloudEvents 1.0 JSON message to a subject. Kafka / SQS are roadmap stubs — selectable for visibility, but the dispatcher rejects subscriptions targeting them today."
              >
                <ToggleRow
                  options={SINK_OPTIONS}
                  selected={form.sinkType}
                  onSelect={(id) => {
                    setForm((p) => ({ ...p, sinkType: id }));
                    setErrors({});
                  }}
                />
              </Field>

              {form.sinkType === "http" && (
                <>
                  <Field
                    label="URL"
                    htmlFor="sub-http-url"
                    hint={
                      TEMPLATES.find((t) => t.id === form.template)?.secretHint
                    }
                    error={errors.httpUrl}
                  >
                    <Input
                      id="sub-http-url"
                      type="url"
                      value={form.httpUrl}
                      placeholder={
                        TEMPLATES.find((t) => t.id === form.template)
                          ?.urlPlaceholder ?? "https://example.com/webhook"
                      }
                      onChange={(e) =>
                        setForm((p) => ({ ...p, httpUrl: e.target.value }))
                      }
                    />
                  </Field>
                  <Field
                    label="Signing secret ref"
                    htmlFor="sub-http-secret"
                    optional
                    hint="Reference to a Secret holding the HMAC-SHA256 signing key. Leave blank for unsigned deliveries."
                  >
                    <Input
                      id="sub-http-secret"
                      value={form.httpSecret}
                      onChange={(e) =>
                        setForm((p) => ({ ...p, httpSecret: e.target.value }))
                      }
                    />
                  </Field>
                  <Field
                    label="Max attempts"
                    htmlFor="sub-http-attempts"
                    hint="PALADIN retries with exponential backoff up to this many times."
                    error={errors.httpMaxAttempts}
                  >
                    <Input
                      id="sub-http-attempts"
                      type="number"
                      min={1}
                      max={10}
                      value={form.httpMaxAttempts}
                      onChange={(e) =>
                        setForm((p) => ({
                          ...p,
                          httpMaxAttempts: e.target.value,
                        }))
                      }
                    />
                  </Field>
                </>
              )}

              {form.sinkType === "nats" && (
                <>
                  <Field
                    label="Server URL"
                    htmlFor="sub-nats-url"
                    hint="nats:// or nats-tls://. Comma-separated for cluster (nats://nats-0:4222,nats://nats-1:4222). One connection is pooled per unique URL across all subscriptions."
                    error={errors.natsUrl}
                  >
                    <Input
                      id="sub-nats-url"
                      value={form.natsUrl}
                      placeholder="nats://nats.nats.svc.cluster.local:4222"
                      onChange={(e) =>
                        setForm((p) => ({ ...p, natsUrl: e.target.value }))
                      }
                    />
                  </Field>
                  <Field
                    label="Subject"
                    htmlFor="sub-nats-subject"
                    hint="Where the CloudEvents JSON payload publishes. Static for v1; convention: paladin.events.<tenant_id>.<event_type>."
                    error={errors.natsSubject}
                  >
                    <Input
                      id="sub-nats-subject"
                      value={form.natsSubject}
                      placeholder="paladin.events"
                      onChange={(e) =>
                        setForm((p) => ({ ...p, natsSubject: e.target.value }))
                      }
                    />
                  </Field>
                  <Field
                    label="Credentials ref"
                    htmlFor="sub-nats-creds"
                    optional
                    hint="Format <scheme>:<value>. v1 supports token:<plaintext>. NKey / JWT BACKLOG. Empty = anonymous (lab clusters only)."
                  >
                    <Input
                      id="sub-nats-creds"
                      value={form.natsCredentialsRef}
                      placeholder="token:..."
                      onChange={(e) =>
                        setForm((p) => ({
                          ...p,
                          natsCredentialsRef: e.target.value,
                        }))
                      }
                    />
                  </Field>
                </>
              )}

              {form.sinkType === "kafka" && (
                <>
                  <Field
                    label="Brokers"
                    htmlFor="sub-kafka-brokers"
                    hint="Comma-separated list, e.g. broker1:9092,broker2:9092."
                    error={errors.kafkaBrokers}
                  >
                    <Input
                      id="sub-kafka-brokers"
                      value={form.kafkaBrokers}
                      onChange={(e) =>
                        setForm((p) => ({
                          ...p,
                          kafkaBrokers: e.target.value,
                        }))
                      }
                    />
                  </Field>
                  <Field
                    label="Topic"
                    htmlFor="sub-kafka-topic"
                    error={errors.kafkaTopic}
                  >
                    <Input
                      id="sub-kafka-topic"
                      value={form.kafkaTopic}
                      onChange={(e) =>
                        setForm((p) => ({ ...p, kafkaTopic: e.target.value }))
                      }
                    />
                  </Field>
                </>
              )}

              {form.sinkType === "sqs" && (
                <>
                  <Field
                    label="Queue URL"
                    htmlFor="sub-sqs-url"
                    error={errors.sqsQueueUrl}
                  >
                    <Input
                      id="sub-sqs-url"
                      type="url"
                      value={form.sqsQueueUrl}
                      onChange={(e) =>
                        setForm((p) => ({
                          ...p,
                          sqsQueueUrl: e.target.value,
                        }))
                      }
                    />
                  </Field>
                  <Field
                    label="Region"
                    htmlFor="sub-sqs-region"
                    error={errors.sqsRegion}
                  >
                    <Input
                      id="sub-sqs-region"
                      value={form.sqsRegion}
                      placeholder="us-east-1"
                      onChange={(e) =>
                        setForm((p) => ({ ...p, sqsRegion: e.target.value }))
                      }
                    />
                  </Field>
                </>
              )}
            </FormSection>

            <FormSection title="Filter (CEL)">
              <Field
                label="Expression"
                htmlFor="sub-filter"
                optional
                hint={
                  "Empty = all events. Examples: event.kind == 'object.uploaded', event.tenant_id == 't_acme' && event.severity == 'error'. CEL evaluates against the EventEnvelope."
                }
              >
                <Textarea
                  id="sub-filter"
                  className={cn(T.code, "min-h-[80px]")}
                  value={form.filter}
                  onChange={(e) =>
                    setForm((p) => ({ ...p, filter: e.target.value }))
                  }
                  placeholder=""
                />
                <CELIndicator state={filterCELState} />
              </Field>
            </FormSection>

            <FormSection title="State">
              <div className="flex items-center justify-between gap-4 rounded-md border border-input p-3">
                <div className="min-w-0">
                  <p className="text-sm font-medium">Disabled</p>
                  <p className={T.hint}>
                    When on, the subscription stops receiving events. Existing
                    in-flight deliveries continue.
                  </p>
                </div>
                <Switch
                  checked={form.disabled}
                  onCheckedChange={(v) =>
                    setForm((p) => ({ ...p, disabled: v }))
                  }
                />
              </div>
            </FormSection>

            {editing && (
              <div className="flex items-center justify-between gap-3 rounded-md border border-input p-3">
                <div className="min-w-0">
                  <p className="text-sm font-medium">Test delivery</p>
                  <p className={T.hint}>
                    Delivers a synthetic <code>paladin.test</code> event to the
                    currently saved sink (HTTP POST, or a CloudEvents publish on
                    the configured NATS subject). Save changes first if
                    you&apos;ve edited URL / subject / topic.
                  </p>
                  {tests.get(editing.name) ? (
                    <div className="mt-1.5">
                      <TestResultDisplay result={tests.get(editing.name)!} />
                    </div>
                  ) : null}
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!editingTestable || pendingTest.has(editing.name)}
                  onClick={() => void handleTest(editing)}
                >
                  <PlayIcon className="size-4" />
                  {pendingTest.has(editing.name) ? "Testing…" : "Test"}
                </Button>
              </div>
            )}
          </div>

          <DialogFooter>
            <Button variant="outline" type="button" onClick={closeEditor}>
              Cancel
            </Button>
            <Button
              type="button"
              onClick={() => void handleSave()}
              disabled={saving || filterCELBlocksSubmit}
            >
              {saving ? "Saving…" : editing ? "Save changes" : "Create"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
