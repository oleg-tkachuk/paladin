"use client";

import { useEffect, useMemo, useState } from "react";
import { PlayIcon } from "@heroicons/react/24/outline";
import { ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
  EMPTY_FORM,
  formFromSubscription,
  buildSink,
  validateForm,
  type FormState,
  type FormErrors,
  type TemplateId,
  type TestResult,
} from "./_form";
import {
  FormSection,
  Field,
  ToggleRow,
  TestResultDisplay,
} from "./_components";

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

  // Initialize the form whenever the dialog opens — EMPTY_FORM for create,
  // the subscription's current config for edit. Mirrors the old page-level
  // openCreate/openEdit which set the form before flipping editorOpen.
  useEffect(() => {
    if (!open) return;
    setForm(editing ? formFromSubscription(editing) : EMPTY_FORM);
    setErrors({});
  }, [open, editing]);

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
      const msg = err instanceof ConnectError ? err.rawMessage : "Save failed";
      showNotification({ type: "error", title: "Save failed", message: msg });
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
  const filterCELBlocksSubmit =
    filterCELState.status === "invalid" ||
    filterCELState.status === "validating";

  return (
    <Dialog open={open} onOpenChange={(o) => (o ? undefined : onClose())}>
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
                  currently saved sink (HTTP POST, or a CloudEvents publish on
                  the configured NATS subject). Save changes first if
                  you&apos;ve edited URL / subject / topic.
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
        </div>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={onClose}>
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
  );
}
