# ADR-0020: The SDKs to integration grade

- **Status:** Accepted 2026-10-02.

- **Context.** ADR-0018 gave both SDKs their layers. A consumer with a real
  deployment — a gateway on mutual TLS reading its token from a store
  operators rotate, ingestion workers on a protobuf-6 framework streaming
  large documents through presigned URLs signed for a public host — still
  kept a hand-written client beside them: the Python SDK could not be
  installed beside protobuf 6, presigned transfers had no transport of their
  own, errors were codes and messages, and nothing let the consumer test
  without a server. The gap analysis is in
  [specs/004-sdk-integration-grade](../../specs/004-sdk-integration-grade/gap-analysis.md).

- **Decision.**
  - **The Python stubs target protobuf 6 and 7.** They are generated with an
    older protoc on purpose: a stub refuses a runtime older than its
    gencode, so the generator's version is the floor. A test keeps the
    stubs, the declared floor and the CI matrix in agreement.
  - **Presigned transfers are their own leg.** A `Transfer`, declared once on
    the client, sends them with bounded stages and no redirects, and can send
    a URL signed for a public host to an internal address with the signed
    `Host` kept. Downloads stream and are verified against the checksum the
    upload records with `CompleteObject`.
  - **TLS from files that rotate.** Both SDKs take a CA bundle and a client
    certificate and re-read them when they change. The Go SDK checks a
    server's SPIFFE ID with the SPIFFE project's `go-spiffe`; the Python SDK
    cannot, because its HTTP stack has no peer-verification hook, and says
    so.
  - **Errors are typed, from a reason in the contract.** The server attaches
    a `google.rpc.ErrorInfo` whose reason is a `paladin.common.v1.ErrorReason`
    to every error it maps from a domain condition, and names its release in
    `X-Paladin-Version` on every response — answering a procedure it lacks
    with a Connect `Unimplemented`, not a bare 404. The SDKs turn these into
    typed errors and a contract-skew error naming both releases.
  - **Names are hand-written, against one table.** Resource names and
    `paladin://` object URIs are built and parsed by the server's rules —
    under a tenant, its id, not its slug — and both SDKs are tested against
    `sdk/testdata/names.json`.
  - **Parity is tested, not intended.** Both SDKs ship an in-memory data
    plane for consumers' tests, and run the scenarios in
    `sdk/testdata/scenarios.json` against the compose stack in CI. Where they
    differ on purpose, a table in each README says so.
  - **No observability dependency.** Hooks and structured logging are the
    SDKs' own; OpenTelemetry comes through connect's and the HTTP stacks'
    own instrumentation, wired through the options there are.

- **Alternatives.** Generating names from `google.api.resource` annotations
  (ADR-0018's proposal): an annotation pattern cannot say that a collection
  may contain `/`, so the generated parsers would be wrong. Reasons as free
  strings: a client could not know the set, and a typo would pass. An
  OpenTelemetry dependency in the SDKs: most consumers bring their own; since
  the move to connectrpc, its `connectrpc-otel` interceptor works through a
  client's `interceptors`.

- **Consequences.** The SDK releases from 0.13.0 on change behaviour
  (docs/upgrading.md): the HTTP client for presigned requests moved to the
  client, downloads are verified, redirects are refused. Each is a `!`
  commit, and the stream's release notes list it under *Behaviour changes*.
  An SDK change now runs the deep CI jobs, where the scenarios run.
