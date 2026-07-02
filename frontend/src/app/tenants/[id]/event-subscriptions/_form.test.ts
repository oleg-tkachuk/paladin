import { describe, expect, it } from "vitest";

import type { EventSubscription } from "@/gen/paladin/admin/v1/types_pb";
import {
  EMPTY_FORM,
  TEMPLATES,
  buildSink,
  formFromSubscription,
  validateForm,
} from "./_form";

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
  it("defaults to CloudEvents when the operator doesn't opt into raw", () => {
    const sink = buildSink({ ...EMPTY_FORM, sinkType: "http" });
    expect(httpTarget(sink)?.format).toBe("cloudevents");
  });

  it("builds the raw format when explicitly selected", () => {
    const sink = buildSink({
      ...EMPTY_FORM,
      sinkType: "http",
      httpFormat: "raw",
    });
    expect(httpTarget(sink)?.format).toBe("raw");
  });

  it("never emits an unknown format string (guards against stray values)", () => {
    const sink = buildSink({
      ...EMPTY_FORM,
      sinkType: "http",
      // Force an out-of-band value past the type to prove the guard.
      httpFormat: "garbage" as never,
    });
    // Only "raw" opts out; anything else (incl. stray values) → cloudevents.
    expect(httpTarget(sink)?.format).toBe("cloudevents");
  });

  it("hydrates httpFormat from an existing subscription (edit round-trip)", () => {
    expect(formFromSubscription(httpSub("cloudevents")).httpFormat).toBe(
      "cloudevents",
    );
    expect(formFromSubscription(httpSub("raw")).httpFormat).toBe("raw");
    // A pre-flip sub with an unset format now hydrates as CloudEvents — the
    // backend already treats "" as the envelope, so the UI must match.
    expect(formFromSubscription(httpSub("")).httpFormat).toBe("cloudevents");
  });
});

// RabbitMQ sink — the connector was API-only (not even in the SinkType
// selector). Round-trip build + hydrate + validation.

function rabbitSub(
  url: string,
  exchange: string,
  routingKey: string,
): EventSubscription {
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
        case: "rabbitmq",
        value: {
          $typeName: "paladin.admin.v1.RabbitMqSink",
          url,
          exchange,
          routingKey,
        },
      },
    },
  } as EventSubscription;
}

const rabbitTarget = (sink: ReturnType<typeof buildSink>) =>
  sink.target.case === "rabbitmq" ? sink.target.value : undefined;

describe("RabbitMQ sink", () => {
  it("builds a RabbitMqSink from the form, trimming fields", () => {
    const sink = buildSink({
      ...EMPTY_FORM,
      sinkType: "rabbitmq",
      rabbitmqUrl: "  amqp://guest:guest@rabbit:5672/  ",
      rabbitmqExchange: " paladin ",
      rabbitmqRoutingKey: " paladin.events ",
    });
    const v = rabbitTarget(sink);
    expect(v?.url).toBe("amqp://guest:guest@rabbit:5672/");
    expect(v?.exchange).toBe("paladin");
    expect(v?.routingKey).toBe("paladin.events");
  });

  it("hydrates the form from an existing RabbitMQ subscription", () => {
    const f = formFromSubscription(
      rabbitSub("amqps://rabbit:5671/", "events", "paladin.q1"),
    );
    expect(f.sinkType).toBe("rabbitmq");
    expect(f.rabbitmqUrl).toBe("amqps://rabbit:5671/");
    expect(f.rabbitmqExchange).toBe("events");
    expect(f.rabbitmqRoutingKey).toBe("paladin.q1");
  });

  it("rejects a non-AMQP URL and a missing routing key", () => {
    const errs = validateForm({
      ...EMPTY_FORM,
      sinkType: "rabbitmq",
      rabbitmqUrl: "http://not-amqp",
      rabbitmqRoutingKey: "",
    });
    expect(errs.rabbitmqUrl).toBeTruthy();
    expect(errs.rabbitmqRoutingKey).toBeTruthy();
  });

  it("accepts a valid amqp:// URL with a routing key", () => {
    const errs = validateForm({
      ...EMPTY_FORM,
      sinkType: "rabbitmq",
      rabbitmqUrl: "amqp://guest:guest@rabbit:5672/",
      rabbitmqRoutingKey: "paladin.events",
    });
    expect(errs.rabbitmqUrl).toBeUndefined();
    expect(errs.rabbitmqRoutingKey).toBeUndefined();
  });
});

// SQS cross-account role_arn — optional; round-trips through build + hydrate.
describe("SQS sink role_arn (cross-account)", () => {
  const sqsTarget = (sink: ReturnType<typeof buildSink>) =>
    sink.target.case === "sqs" ? sink.target.value : undefined;

  it("builds the optional roleArn (trimmed); empty stays empty", () => {
    const withRole = sqsTarget(
      buildSink({
        ...EMPTY_FORM,
        sinkType: "sqs",
        sqsQueueUrl: "https://sqs.us-east-1.amazonaws.com/999/q",
        sqsRegion: "us-east-1",
        sqsRoleArn: "  arn:aws:iam::999:role/deliver  ",
      }),
    );
    expect(withRole?.roleArn).toBe("arn:aws:iam::999:role/deliver");

    const noRole = sqsTarget(
      buildSink({
        ...EMPTY_FORM,
        sinkType: "sqs",
        sqsQueueUrl: "https://sqs.us-east-1.amazonaws.com/123/q",
        sqsRegion: "us-east-1",
      }),
    );
    expect(noRole?.roleArn).toBe("");
  });

  it("hydrates roleArn from an existing subscription", () => {
    const f = formFromSubscription({
      $typeName: "paladin.admin.v1.EventSubscription",
      name: "tenants/t-1/eventSubscriptions/s1",
      tenantId: "t-1",
      filter: "",
      disabled: false,
      resourceVersion: "v1",
      sink: {
        $typeName: "paladin.admin.v1.EventSink",
        target: {
          case: "sqs",
          value: {
            $typeName: "paladin.admin.v1.SqsSink",
            queueUrl: "https://sqs.eu-west-1.amazonaws.com/999/q",
            region: "eu-west-1",
            roleArn: "arn:aws:iam::999:role/deliver",
          },
        },
      },
    } as EventSubscription);
    expect(f.sinkType).toBe("sqs");
    expect(f.sqsRoleArn).toBe("arn:aws:iam::999:role/deliver");
  });
});

// Kafka SASL / TLS auth — build + hydrate + validation. mTLS client cert/key
// are API-only, so the UI always builds them empty.
describe("Kafka sink auth", () => {
  const kafkaTarget = (sink: ReturnType<typeof buildSink>) =>
    sink.target.case === "kafka" ? sink.target.value : undefined;

  it("builds SASL_SSL (SCRAM-256 + TLS), leaving mTLS PEM empty", () => {
    const v = kafkaTarget(
      buildSink({
        ...EMPTY_FORM,
        sinkType: "kafka",
        kafkaBrokers: "b1:9092,b2:9092",
        kafkaTopic: "paladin.events",
        kafkaSaslMechanism: "scram-sha-256",
        kafkaSaslUsername: "  svc-paladin  ",
        kafkaSaslPassword: "s3cret",
        kafkaTlsEnabled: true,
      }),
    );
    expect(v?.saslMechanism).toBe("scram-sha-256");
    expect(v?.saslUsername).toBe("svc-paladin"); // trimmed
    expect(v?.saslPassword).toBe("s3cret"); // not trimmed
    expect(v?.tlsEnabled).toBe(true);
    expect(v?.tlsClientCert).toBe("");
    expect(v?.tlsClientKey).toBe("");
  });

  it("hydrates the auth fields from an existing kafka subscription", () => {
    const f = formFromSubscription({
      $typeName: "paladin.admin.v1.EventSubscription",
      name: "tenants/t-1/eventSubscriptions/s1",
      tenantId: "t-1",
      filter: "",
      disabled: false,
      resourceVersion: "v1",
      sink: {
        $typeName: "paladin.admin.v1.EventSink",
        target: {
          case: "kafka",
          value: {
            $typeName: "paladin.admin.v1.KafkaSink",
            brokers: "b:9092",
            topic: "t",
            saslMechanism: "plain",
            saslUsername: "u",
            saslPassword: "p",
            tlsEnabled: true,
            tlsClientCert: "",
            tlsClientKey: "",
          },
        },
      },
    } as EventSubscription);
    expect(f.sinkType).toBe("kafka");
    expect(f.kafkaSaslMechanism).toBe("plain");
    expect(f.kafkaSaslUsername).toBe("u");
    expect(f.kafkaTlsEnabled).toBe(true);
  });

  it("requires a SASL username once a mechanism is chosen", () => {
    const errs = validateForm({
      ...EMPTY_FORM,
      sinkType: "kafka",
      kafkaBrokers: "b:9092",
      kafkaTopic: "t",
      kafkaSaslMechanism: "plain",
      kafkaSaslUsername: "",
    });
    expect(errs.kafkaSaslUsername).toBeTruthy();
  });

  it("no SASL selected → no auth-field errors", () => {
    const errs = validateForm({
      ...EMPTY_FORM,
      sinkType: "kafka",
      kafkaBrokers: "b:9092",
      kafkaTopic: "t",
    });
    expect(errs.kafkaSaslUsername).toBeUndefined();
  });
});

// ─── Connector templates ─────────────────────────────────────────────────────

describe("connector templates", () => {
  it("Redpanda pins the Kafka sink and prefills a broker placeholder", () => {
    const rp = TEMPLATES.find((t) => t.id === "redpanda");
    expect(rp).toBeDefined();
    // Redpanda is Kafka-wire — the preset must pin the Kafka sink so its
    // fields (brokers / topic / SASL / TLS) render.
    expect(rp?.pinnedSinkType).toBe("kafka");
    expect(rp?.brokersPlaceholder).toMatch(/:\d+$/);
  });

  it("webhook connectors pin HTTP; Custom pins nothing", () => {
    for (const id of ["slack", "discord", "pagerduty", "plain-http"]) {
      expect(TEMPLATES.find((t) => t.id === id)?.pinnedSinkType).toBe("http");
    }
    expect(
      TEMPLATES.find((t) => t.id === "custom")?.pinnedSinkType,
    ).toBeUndefined();
  });
});
