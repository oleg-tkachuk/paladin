"use client";

import { useMemo, useState } from "react";
import { PlayIcon } from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { FormDialog, FormSection } from "@/components/ui/form-dialog";
import { useNotification } from "@/components/ui/Notification";
import { CELIndicator } from "@/components/ui/CELIndicator";
import { useCELValidation } from "@/hooks/useCELValidation";
import { eventSubscriptionClient } from "@/lib/connect/client";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { EventSubscription } from "@/gen/paladin/admin/v1/types_pb";

import {
  TEMPLATES,
  SINK_OPTIONS,
  sinkTypeLabel,
  HTTP_FORMAT_OPTIONS,
  KAFKA_SASL_OPTIONS,
  EMPTY_FORM,
  formFromSubscription,
  buildSink,
  validateForm,
  type FormState,
  type FormErrors,
  type TemplateId,
  type TestResult,
} from "./_form";
import { Field, ToggleRow, TestResultDisplay } from "./_components";
import { errorMessage } from "@/hooks/errorContract";

/**
 * Create/edit dialog for an event subscription, extracted from the page. Owns
 * the form, validation errors, save-in-flight state, the live CEL filter
 * validation, and the create/update RPC. The page owns the open/editing target
 * and the shared test state (test delivery is also triggerable from the row
 * menu), wiring those in via props:
 *   - onSaved(result, wasEdit) — merge the created/updated row into the list
 *   - onClose()                — clear the open flag + editing target
 *   - onStale()                — refetch after an Aborted (resource-version) save
 *   - testResult/testing/onTest — the inline "Test delivery" panel (edit only)
 */
export function SubscriptionEditorDialog({
  open,
  editing,
  tenantId,
  onClose,
  onSaved,
  onStale,
  testResult,
  testing,
  onTest,
}: {
  open: boolean;
  editing: EventSubscription | null;
  tenantId: string;
  onClose: () => void;
  onSaved: (result: EventSubscription, wasEdit: boolean) => void;
  onStale: () => void;
  testResult?: TestResult;
  testing: boolean;
  onTest: () => void;
}) {
  const { showNotification } = useNotification();
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [errors, setErrors] = useState<FormErrors>({});
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Initialize the form whenever the dialog opens — EMPTY_FORM for create,
  // the subscription's current config for edit. Render-phase adjust-on-change
  // keyed on (open, editing) (not a set-state-in-effect); only reseeds while
  // open, so closing doesn't clobber the form mid-animation.
  const [seedKey, setSeedKey] = useState({ open, editing });
  if (seedKey.open !== open || seedKey.editing !== editing) {
    setSeedKey({ open, editing });
    if (open) {
      setForm(editing ? formFromSubscription(editing) : EMPTY_FORM);
      setErrors({});
      setSaveError(null);
    }
  }

  const applyTemplate = (id: TemplateId) => {
    const tpl = TEMPLATES.find((t) => t.id === id);
    if (!tpl) return;
    setForm((prev) => ({
      ...prev,
      template: id,
      sinkType: tpl.pinnedSinkType ?? prev.sinkType,
      filter: tpl.filter !== undefined ? tpl.filter : prev.filter,
    }));
  };

  const handleSave = async () => {
    if (!tenantId) return;
    const e = validateForm(form);
    setErrors(e);
    if (Object.keys(e).length > 0) return;

    setSaveError(null);
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
        onSaved(updated, true);
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
        onSaved(created, false);
        showNotification({
          type: "success",
          title: "Subscription created",
          message: "Event sink is live.",
        });
      }
      onClose();
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Aborted) {
        showNotification({
          type: "warning",
          title: "Stale, please retry",
          message: "Subscription was modified concurrently — refetching.",
        });
        onStale();
        return;
      }
      setSaveError(errorMessage(err, "Save failed"));
    } finally {
      setSaving(false);
    }
  };

  // Submitable in editor? Used by the inline Test button.
  const editingTestable = useMemo(() => {
    if (!editing) return false;
    return Object.keys(validateForm(form)).length === 0;
  }, [editing, form]);

  // Live CEL validation against the EventEnvelope schema. Mirrors the
  // lifecycle editor: keep submit + Test disabled while in flight or on
  // a compile error so operators learn about bad filters at edit-time
  // rather than at first event delivery.
  const filterCELState = useCELValidation(form.filter, "EventEnvelope");

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      title={editing ? "Edit subscription" : "New event subscription"}
      description={
        editing
          ? "The sink and the CEL filter for this subscription."
          : "Forward this tenant's events to a webhook or a broker."
      }
      width="lg"
      onSubmit={() => void handleSave()}
      submitLabel={editing ? "Save changes" : "Create subscription"}
      submittingLabel="Saving…"
      submitting={saving}
      blockedReason={
        filterCELState.status === "validating"
          ? "Checking the filter…"
          : filterCELState.status === "invalid"
            ? "Fix the filter to continue."
            : null
      }
      error={saveError}
    >
      {!editing && (
        <FormSection title="Template">
          <Field
            label="Connector"
            hint="Pick a known connector to pin the sink type and prefill placeholders + a recommended CEL filter (Slack/Discord/PagerDuty → HTTP, Redpanda → Kafka). Pick Custom for full manual control."
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
          hint="HTTP delivers an HMAC-signed POST (raw or CloudEvents). NATS / Kafka / SQS / RabbitMQ publish the CloudEvents 1.0 envelope — to a subject, topic (keyed by tenant), FIFO queue, or exchange/routing-key respectively. All are delivery-wired."
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

        {/* When a connector pins a sink type that isn't the obvious
                same-named one (Redpanda → Kafka), spell out why — so the
                Kafka fields lighting up under a "Redpanda" connector reads
                as intended, not a mis-click. */}
        {(() => {
          const tpl = TEMPLATES.find((t) => t.id === form.template);
          if (
            !tpl?.pinnedSinkType ||
            tpl.pinnedSinkType !== form.sinkType ||
            tpl.pinnedSinkType === "http"
          ) {
            return null;
          }
          return (
            <p className="text-xs text-muted-foreground">
              {tpl.label} speaks the {sinkTypeLabel(tpl.pinnedSinkType)} wire
              protocol — it delivers through the{" "}
              {sinkTypeLabel(tpl.pinnedSinkType)} sink, configured below.
            </p>
          );
        })()}

        {form.sinkType === "http" && (
          <>
            <Field
              required
              label="URL"
              htmlFor="sub-http-url"
              hint={TEMPLATES.find((t) => t.id === form.template)?.secretHint}
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
              required
              label="Max attempts"
              htmlFor="sub-http-attempts"
              hint="Paladin retries with exponential backoff up to this many times."
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
            <Field
              label="Payload format"
              hint="CloudEvents 1.0 (default) wraps the event in the same envelope the NATS/Kafka/SQS sinks emit (Content-Type application/cloudevents+json). Raw JSON posts the bare Event — legacy shape, for subscribers that predate the default."
            >
              <ToggleRow
                options={HTTP_FORMAT_OPTIONS}
                selected={form.httpFormat}
                onSelect={(id) => setForm((p) => ({ ...p, httpFormat: id }))}
              />
            </Field>
          </>
        )}

        {form.sinkType === "nats" && (
          <>
            <Field
              required
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
              required
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
            <Field
              label="JetStream"
              hint="Publish durably onto a JetStream stream with server-side dedup (Nats-Msg-Id = the CloudEvents id), instead of core fire-and-forget publish. The subject must fall under a provisioned stream (e.g. paladin.events.>)."
            >
              <div className="flex items-center gap-2">
                <Switch
                  checked={form.natsJetStream}
                  onCheckedChange={(v) =>
                    setForm((p) => ({ ...p, natsJetStream: v }))
                  }
                />
                <span className={T.hint}>
                  {form.natsJetStream ? "Durable (JetStream)" : "Core publish"}
                </span>
              </div>
            </Field>
          </>
        )}

        {form.sinkType === "kafka" && (
          <>
            <Field
              required
              label="Brokers"
              htmlFor="sub-kafka-brokers"
              hint="Comma-separated list, e.g. broker1:9092,broker2:9092."
              error={errors.kafkaBrokers}
            >
              <Input
                id="sub-kafka-brokers"
                placeholder={
                  TEMPLATES.find((t) => t.id === form.template)
                    ?.brokersPlaceholder ?? "broker1:9092,broker2:9092"
                }
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
              required
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
            <Field
              label="SASL"
              hint="Broker authentication. SASL_SSL = pick a mechanism AND enable TLS below. mTLS client certs are API/config-only."
            >
              <ToggleRow
                options={KAFKA_SASL_OPTIONS}
                selected={
                  (["plain", "scram-sha-256", "scram-sha-512"].includes(
                    form.kafkaSaslMechanism,
                  )
                    ? form.kafkaSaslMechanism
                    : "") as "" | "plain" | "scram-sha-256" | "scram-sha-512"
                }
                onSelect={(id) =>
                  setForm((p) => ({ ...p, kafkaSaslMechanism: id }))
                }
              />
            </Field>
            {form.kafkaSaslMechanism && (
              <>
                <Field
                  required
                  label="SASL username"
                  htmlFor="sub-kafka-sasl-user"
                  error={errors.kafkaSaslUsername}
                >
                  <Input
                    id="sub-kafka-sasl-user"
                    value={form.kafkaSaslUsername}
                    onChange={(e) =>
                      setForm((p) => ({
                        ...p,
                        kafkaSaslUsername: e.target.value,
                      }))
                    }
                  />
                </Field>
                <Field label="SASL password" htmlFor="sub-kafka-sasl-pass">
                  <Input
                    id="sub-kafka-sasl-pass"
                    type="password"
                    value={form.kafkaSaslPassword}
                    onChange={(e) =>
                      setForm((p) => ({
                        ...p,
                        kafkaSaslPassword: e.target.value,
                      }))
                    }
                  />
                </Field>
              </>
            )}
            <Field
              label="TLS"
              hint="Wrap the broker connection in TLS (server verified via system root CAs)."
            >
              <div className="flex items-center gap-2">
                <Switch
                  checked={form.kafkaTlsEnabled}
                  onCheckedChange={(v) =>
                    setForm((p) => ({ ...p, kafkaTlsEnabled: v }))
                  }
                />
                <span className={T.hint}>
                  {form.kafkaTlsEnabled ? "Enabled" : "Plaintext"}
                </span>
              </div>
            </Field>
          </>
        )}

        {form.sinkType === "sqs" && (
          <>
            <Field
              required
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
              required
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
            <Field
              label="Assume-role ARN"
              htmlFor="sub-sqs-role-arn"
              hint="For cross-account delivery: the dispatcher sts:AssumeRole's into this role before sending. Empty = deliver with the dispatcher's ambient (IRSA / env) credentials."
            >
              <Input
                id="sub-sqs-role-arn"
                value={form.sqsRoleArn}
                placeholder="arn:aws:iam::123456789012:role/paladin-sqs-delivery"
                onChange={(e) =>
                  setForm((p) => ({ ...p, sqsRoleArn: e.target.value }))
                }
              />
            </Field>
          </>
        )}

        {form.sinkType === "rabbitmq" && (
          <>
            <Field
              required
              label="AMQP URL"
              htmlFor="sub-rabbitmq-url"
              hint="amqp:// or amqps://. Credentials ride in the URL userinfo (amqp://user:pass@host:5672/vhost)."
              error={errors.rabbitmqUrl}
            >
              <Input
                id="sub-rabbitmq-url"
                value={form.rabbitmqUrl}
                onChange={(e) =>
                  setForm((p) => ({ ...p, rabbitmqUrl: e.target.value }))
                }
              />
            </Field>
            <Field
              label="Exchange"
              htmlFor="sub-rabbitmq-exchange"
              hint="Empty = the default (nameless) exchange, in which case the routing key is the destination queue name."
            >
              <Input
                id="sub-rabbitmq-exchange"
                value={form.rabbitmqExchange}
                placeholder="(default exchange)"
                onChange={(e) =>
                  setForm((p) => ({
                    ...p,
                    rabbitmqExchange: e.target.value,
                  }))
                }
              />
            </Field>
            <Field
              required
              label="Routing key"
              htmlFor="sub-rabbitmq-routing-key"
              error={errors.rabbitmqRoutingKey}
            >
              <Input
                id="sub-rabbitmq-routing-key"
                value={form.rabbitmqRoutingKey}
                placeholder="paladin.events"
                onChange={(e) =>
                  setForm((p) => ({
                    ...p,
                    rabbitmqRoutingKey: e.target.value,
                  }))
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
          hint={
            // Identifiers are bare: the EventEnvelope schema declares
            // kind, tenant_id and severity, not event.kind. The examples
            // carried an `event.` prefix that the validator rejects, so
            // anyone copying them got "undeclared reference to 'event'".
            "Empty = all events. Examples: kind == 'object.uploaded', tenant_id == 't_acme' && severity == 'error'. CEL evaluates against the EventEnvelope."
          }
        >
          <Textarea
            id="sub-filter"
            className={cn(T.code, "min-h-[80px]")}
            value={form.filter}
            onChange={(e) => setForm((p) => ({ ...p, filter: e.target.value }))}
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
            onCheckedChange={(v) => setForm((p) => ({ ...p, disabled: v }))}
          />
        </div>
      </FormSection>

      {editing && (
        <div className="flex items-center justify-between gap-3 rounded-md border border-input p-3">
          <div className="min-w-0">
            <p className="text-sm font-medium">Test delivery</p>
            <p className={T.hint}>
              Delivers a synthetic <code>paladin.test</code> event to the
              currently saved sink (HTTP POST, or a CloudEvents publish on the
              configured NATS subject). Save changes first if you&apos;ve edited
              URL / subject / topic.
            </p>
            {testResult ? (
              <div className="mt-1.5">
                <TestResultDisplay result={testResult} />
              </div>
            ) : null}
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!editingTestable || testing}
            onClick={onTest}
          >
            <PlayIcon className="size-4" />
            {testing ? "Testing…" : "Test"}
          </Button>
        </div>
      )}
    </FormDialog>
  );
}
