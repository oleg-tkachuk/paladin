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
import {
  CheckCircleIcon as CheckCircleSolid,
  ExclamationTriangleIcon as ExclamationSolid,
  XCircleIcon as XCircleSolid,
} from "@heroicons/react/20/solid";
import { ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import { useScope } from "@/context/ScopeContext";
import { eventSubscriptionClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { useCELValidation } from "@/hooks/useCELValidation";
import { CELIndicator } from "@/components/ui/CELIndicator";
import type {
  EventSubscription,
  EventSink,
  HttpSink,
  KafkaSink,
  NatsSink,
  SqsSink,
} from "@/gen/paladin/admin/v1/types_pb";

// ─── Connector templates ──────────────────────────────────────────────
// Templates are pure UI sugar — they prefill the HTTP sink with a
// sensible URL placeholder, secret hint, and default CEL filter for
// well-known consumers. The persisted EventSubscription has no notion
// of "template"; it's just an HttpSink with whatever URL the user
// pastes. "custom" is the no-prefill option for everything else.
type TemplateId = "slack" | "discord" | "pagerduty" | "plain-http" | "custom";

interface TemplateDef {
  id: TemplateId;
  label: string;
  /** When non-null, the template auto-pins the sink type to HTTP. */
  forcesHttp: boolean;
  urlPlaceholder?: string;
  secretHint?: string;
  filter?: string;
  description?: string;
}

const TEMPLATES: readonly TemplateDef[] = [
  {
    id: "slack",
    label: "Slack",
    forcesHttp: true,
    urlPlaceholder: "https://hooks.slack.com/services/...",
    secretHint: "Slack's incoming-webhook URL — paste it as-is.",
    filter: "",
  },
  {
    id: "discord",
    label: "Discord",
    forcesHttp: true,
    urlPlaceholder: "https://discord.com/api/webhooks/...",
    secretHint: "Discord webhook URL.",
    filter: "",
  },
  {
    id: "pagerduty",
    label: "PagerDuty",
    forcesHttp: true,
    urlPlaceholder: "https://events.pagerduty.com/v2/enqueue",
    secretHint:
      "PagerDuty Events API V2 endpoint. Routing key goes in the request body — set signing_secret_ref to the integration key Secret.",
    filter: "event.severity in ['error','critical']",
  },
  {
    id: "plain-http",
    label: "Plain HTTP",
    forcesHttp: true,
    urlPlaceholder: "https://example.com/webhook",
    secretHint: "Any HTTPS endpoint.",
    filter: "",
  },
  {
    id: "custom",
    label: "Custom",
    forcesHttp: false,
  },
] as const;

type SinkType = "http" | "nats" | "kafka" | "sqs";

// NATS sits second after HTTP — it's the second wired sink (Kafka/SQS
// remain "not yet wired" stubs that operator pickers can still see for
// visibility into roadmap, but EventSubscriptionService.Validate
// rejects subs targeting them today).
const SINK_OPTIONS: readonly { id: SinkType; label: string }[] = [
  { id: "http", label: "HTTP" },
  { id: "nats", label: "NATS" },
  { id: "kafka", label: "Kafka" },
  { id: "sqs", label: "SQS" },
] as const;

type TestResult = {
  delivered: boolean;
  statusCode: number;
  errorMessage: string;
  at: number;
};

// ─── Local helpers ────────────────────────────────────────────────────
function FormSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-3">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {title}
      </h3>
      <div className="space-y-3">{children}</div>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  optional,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor?: string;
  optional?: boolean;
  hint?: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs">
        {label}
        {optional && (
          <span className="ml-1.5 font-normal text-muted-foreground">
            (optional)
          </span>
        )}
      </Label>
      {children}
      {error ? (
        <p className={cn(T.hint, "text-destructive")}>{error}</p>
      ) : hint ? (
        <p className={T.hint}>{hint}</p>
      ) : null}
    </div>
  );
}

function ToggleRow<T extends string>({
  options,
  selected,
  onSelect,
}: {
  options: readonly { id: T; label: string }[];
  selected: T | null;
  onSelect: (value: T) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((opt) => {
        const active = selected === opt.id;
        return (
          <button
            key={opt.id}
            type="button"
            onClick={() => onSelect(opt.id)}
            aria-pressed={active}
            className={cn(
              "rounded-md border px-2.5 py-1 text-xs transition-colors",
              active
                ? "border-primary bg-primary/10 text-primary"
                : "border-input bg-background hover:bg-muted",
            )}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

function isValidHttpUrl(s: string): boolean {
  if (!s) return false;
  try {
    const u = new URL(s);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

function isValidUrl(s: string): boolean {
  if (!s) return false;
  try {
    new URL(s);
    return true;
  } catch {
    return false;
  }
}

function sinkSummary(sub: EventSubscription): {
  badge: string;
  detail: string;
} {
  const t = sub.sink?.target;
  if (t?.case === "http") {
    return { badge: "HTTP", detail: t.value.url || "—" };
  }
  if (t?.case === "nats") {
    const n = t.value;
    // Show subject prominently — it's the discriminator a subscriber
    // configures their listener with. URL is auxiliary; truncated
    // visually by the table cell anyway.
    return { badge: "NATS", detail: `${n.subject} @ ${n.url}` };
  }
  if (t?.case === "kafka") {
    const k = t.value;
    return { badge: "Kafka", detail: `${k.topic} @ ${k.brokers}` };
  }
  if (t?.case === "sqs") {
    return { badge: "SQS", detail: t.value.queueUrl };
  }
  return { badge: "—", detail: "—" };
}

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n - 1) + "…";
}

// ─── Edit form state ──────────────────────────────────────────────────
type FormState = {
  template: TemplateId;
  sinkType: SinkType;
  // HTTP
  httpUrl: string;
  httpSecret: string;
  httpMaxAttempts: string;
  // NATS
  natsUrl: string;
  natsSubject: string;
  natsCredentialsRef: string;
  // Kafka
  kafkaBrokers: string;
  kafkaTopic: string;
  // SQS
  sqsQueueUrl: string;
  sqsRegion: string;
  // Common
  filter: string;
  disabled: boolean;
};

const EMPTY_FORM: FormState = {
  template: "custom",
  sinkType: "http",
  httpUrl: "",
  httpSecret: "",
  httpMaxAttempts: "5",
  natsUrl: "nats://nats.nats.svc.cluster.local:4222",
  natsSubject: "paladin.events",
  natsCredentialsRef: "",
  kafkaBrokers: "",
  kafkaTopic: "",
  sqsQueueUrl: "",
  sqsRegion: "",
  filter: "",
  disabled: false,
};

function formFromSubscription(sub: EventSubscription): FormState {
  const t = sub.sink?.target;
  const next: FormState = {
    ...EMPTY_FORM,
    filter: sub.filter,
    disabled: sub.disabled,
  };
  if (t?.case === "http") {
    next.sinkType = "http";
    next.httpUrl = t.value.url;
    next.httpSecret = t.value.signingSecretRef;
    next.httpMaxAttempts = String(t.value.maxAttempts || 5);
  } else if (t?.case === "nats") {
    next.sinkType = "nats";
    next.natsUrl = t.value.url;
    next.natsSubject = t.value.subject;
    next.natsCredentialsRef = t.value.credentialsRef;
  } else if (t?.case === "kafka") {
    next.sinkType = "kafka";
    next.kafkaBrokers = t.value.brokers;
    next.kafkaTopic = t.value.topic;
  } else if (t?.case === "sqs") {
    next.sinkType = "sqs";
    next.sqsQueueUrl = t.value.queueUrl;
    next.sqsRegion = t.value.region;
  }
  return next;
}

function buildSink(form: FormState): EventSink {
  if (form.sinkType === "http") {
    const http: HttpSink = {
      $typeName: "paladin.admin.v1.HttpSink",
      url: form.httpUrl.trim(),
      signingSecretRef: form.httpSecret.trim(),
      maxAttempts: Number.parseInt(form.httpMaxAttempts, 10) || 5,
    };
    return {
      $typeName: "paladin.admin.v1.EventSink",
      target: { case: "http", value: http },
    };
  }
  if (form.sinkType === "nats") {
    const nats: NatsSink = {
      $typeName: "paladin.admin.v1.NatsSink",
      url: form.natsUrl.trim(),
      subject: form.natsSubject.trim(),
      credentialsRef: form.natsCredentialsRef.trim(),
    };
    return {
      $typeName: "paladin.admin.v1.EventSink",
      target: { case: "nats", value: nats },
    };
  }
  if (form.sinkType === "kafka") {
    const kafka: KafkaSink = {
      $typeName: "paladin.admin.v1.KafkaSink",
      brokers: form.kafkaBrokers.trim(),
      topic: form.kafkaTopic.trim(),
    };
    return {
      $typeName: "paladin.admin.v1.EventSink",
      target: { case: "kafka", value: kafka },
    };
  }
  const sqs: SqsSink = {
    $typeName: "paladin.admin.v1.SqsSink",
    queueUrl: form.sqsQueueUrl.trim(),
    region: form.sqsRegion.trim(),
  };
  return {
    $typeName: "paladin.admin.v1.EventSink",
    target: { case: "sqs", value: sqs },
  };
}

interface FormErrors {
  httpUrl?: string;
  httpMaxAttempts?: string;
  natsUrl?: string;
  natsSubject?: string;
  kafkaBrokers?: string;
  kafkaTopic?: string;
  sqsQueueUrl?: string;
  sqsRegion?: string;
}

function validateForm(form: FormState): FormErrors {
  const e: FormErrors = {};
  if (form.sinkType === "http") {
    if (!isValidHttpUrl(form.httpUrl.trim())) {
      e.httpUrl = "Must be a valid http(s):// URL.";
    }
    const n = Number.parseInt(form.httpMaxAttempts, 10);
    if (!Number.isFinite(n) || n < 1 || n > 10) {
      e.httpMaxAttempts = "Must be an integer between 1 and 10.";
    }
  } else if (form.sinkType === "nats") {
    const url = form.natsUrl.trim();
    // NATS URL: nats:// or nats-tls:// (TLS-flavoured). Plain hostname
    // OR cluster list (comma-separated). Loose check — defer real
    // validation to the dispatcher's connect-time error which surfaces
    // on the row's last_error.
    if (!/^nats(-tls)?:\/\//.test(url)) {
      e.natsUrl = "Must start with nats:// or nats-tls://";
    }
    if (!form.natsSubject.trim()) e.natsSubject = "Required.";
  } else if (form.sinkType === "kafka") {
    if (!form.kafkaBrokers.trim()) e.kafkaBrokers = "Required.";
    if (!form.kafkaTopic.trim()) e.kafkaTopic = "Required.";
  } else if (form.sinkType === "sqs") {
    if (!isValidUrl(form.sqsQueueUrl.trim()))
      e.sqsQueueUrl = "Must be a valid URL.";
    if (!form.sqsRegion.trim()) e.sqsRegion = "Required.";
  }
  return e;
}

// ─── Test result inline display ───────────────────────────────────────
function TestResultDisplay({ result }: { result: TestResult }) {
  // NATS / non-HTTP sinks: dispatcher returns statusCode=0 on success
  // because the protocol has no broker-level ack analogous to an HTTP
  // 2xx. Treat 0 as "delivered, no status code applicable" and drop
  // the parenthesised number so the chip doesn't read "Delivered (0)".
  if (result.delivered && result.statusCode === 0) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-chart-2">
        <CheckCircleSolid className="size-4" />
        Delivered
      </span>
    );
  }
  if (result.delivered && result.statusCode === 200) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-chart-2">
        <CheckCircleSolid className="size-4" />
        Delivered ({result.statusCode})
      </span>
    );
  }
  if (result.delivered && result.statusCode >= 400) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-chart-3">
        <ExclamationSolid className="size-4" />
        Reached endpoint, HTTP {result.statusCode}
      </span>
    );
  }
  if (result.delivered) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <CheckCircleSolid className="size-4" />
        Delivered ({result.statusCode})
      </span>
    );
  }
  return (
    <span
      className="inline-flex items-center gap-1.5 text-xs text-destructive"
      title={result.errorMessage}
    >
      <XCircleSolid className="size-4" />
      Failed: {truncate(result.errorMessage || "unknown error", 64)}
    </span>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────
export default function EventsPage() {
  const { tenantId } = useScope();
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
    <div className="space-y-6">
      <PageHeader
        title="Event subscriptions"
        description="Outbound event sinks for this tenant"
        showDefaultActions={false}
        actions={
          <Button onClick={openCreate} size="sm">
            <PlusIcon className="size-4" />
            New subscription
          </Button>
        }
      />

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
                    <div>
                      <p className="text-sm font-medium">
                        No event subscriptions
                      </p>
                      <p className={cn(T.helper, "mt-1 max-w-md")}>
                        Forward PALADIN events to webhooks, Kafka topics, or SQS
                        queues. Create one to start receiving deliveries.
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
