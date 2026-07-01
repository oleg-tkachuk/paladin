// Pure form logic, connector templates, and validation for the event
// subscription editor. Extracted from page.tsx so the editor dialog and the
// list page can share it without dragging in JSX. No React here.
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
export type TemplateId =
  "slack" | "discord" | "pagerduty" | "plain-http" | "custom";

export interface TemplateDef {
  id: TemplateId;
  label: string;
  /** When non-null, the template auto-pins the sink type to HTTP. */
  forcesHttp: boolean;
  urlPlaceholder?: string;
  secretHint?: string;
  filter?: string;
  description?: string;
}

export const TEMPLATES: readonly TemplateDef[] = [
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

export type SinkType = "http" | "nats" | "kafka" | "sqs";

// HTTP, NATS, Kafka, and SQS sinks are all delivery-wired (2026-06-30) and
// have no create-time restriction. RabbitMQ is also wired in the backend but
// has no form fields here yet, so it's omitted from the picker (API-only for
// now — tracked under the RabbitMQ-sink BACKLOG follow-up).
export const SINK_OPTIONS: readonly { id: SinkType; label: string }[] = [
  { id: "http", label: "HTTP" },
  { id: "nats", label: "NATS" },
  { id: "kafka", label: "Kafka" },
  { id: "sqs", label: "SQS" },
] as const;

// HTTP sink payload format (HttpSink.format). "" is the legacy raw Event JSON;
// "cloudevents" wraps it in the CloudEvents 1.0 envelope like the broker sinks.
export const HTTP_FORMAT_OPTIONS: readonly {
  id: "" | "cloudevents";
  label: string;
}[] = [
  { id: "", label: "Raw JSON" },
  { id: "cloudevents", label: "CloudEvents 1.0" },
] as const;

export type TestResult = {
  delivered: boolean;
  statusCode: number;
  errorMessage: string;
  at: number;
};

export function isValidHttpUrl(s: string): boolean {
  if (!s) return false;
  try {
    const u = new URL(s);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

export function isValidUrl(s: string): boolean {
  if (!s) return false;
  try {
    new URL(s);
    return true;
  } catch {
    return false;
  }
}

export function sinkSummary(sub: EventSubscription): {
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

export function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n - 1) + "…";
}

// ─── Edit form state ──────────────────────────────────────────────────
export type FormState = {
  template: TemplateId;
  sinkType: SinkType;
  // HTTP
  httpUrl: string;
  httpSecret: string;
  httpMaxAttempts: string;
  // "" (raw legacy Event JSON) | "cloudevents" (CloudEvents 1.0 envelope,
  // matching the broker sinks). See HttpSink.format.
  httpFormat: string;
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

export const EMPTY_FORM: FormState = {
  template: "custom",
  sinkType: "http",
  httpUrl: "",
  httpSecret: "",
  httpMaxAttempts: "5",
  httpFormat: "",
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

export function formFromSubscription(sub: EventSubscription): FormState {
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
    next.httpFormat = t.value.format || "";
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

export function buildSink(form: FormState): EventSink {
  if (form.sinkType === "http") {
    const http: HttpSink = {
      $typeName: "paladin.admin.v1.HttpSink",
      url: form.httpUrl.trim(),
      signingSecretRef: form.httpSecret.trim(),
      maxAttempts: Number.parseInt(form.httpMaxAttempts, 10) || 5,
      // "" = legacy raw Event JSON (default, unchanged behavior).
      // "cloudevents" = CloudEvents 1.0 envelope, matching the broker sinks.
      format: form.httpFormat === "cloudevents" ? "cloudevents" : "",
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

export interface FormErrors {
  httpUrl?: string;
  httpMaxAttempts?: string;
  natsUrl?: string;
  natsSubject?: string;
  kafkaBrokers?: string;
  kafkaTopic?: string;
  sqsQueueUrl?: string;
  sqsRegion?: string;
}

export function validateForm(form: FormState): FormErrors {
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
