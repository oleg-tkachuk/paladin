# ADR-0018: The SDKs as three layers over the generated clients

- **Status:** Accepted 2026-10-02 for the client-side layers, which land with
  this ADR in both SDKs. The parts that need the contract or the server to
  change are **Proposed** and tracked in BACKLOG.md under *SDK*.

- **Context.** Both SDKs were a thin shim over the generated Connect clients:
  a base URL, a static token, a context-scoped idempotency key, and retries.
  What a caller actually does took more than that, and every caller rebuilt
  it:

  - the server refuses `Create*`/`Issue*` without an `Idempotency-Key`, and
    the SDKs sent none unless asked, so `CreateTenant` failed out of the box;
    the MCP bridge carried its own interceptor to work around it;
  - a token is issued for one plane and refused by the others, and access
    tokens are short-lived; the console's BFF exchanges and refreshes them,
    an SDK user had to write the same;
  - three planes and 27 services meant one client and 27 constructors by
    hand, which the MCP bridge also rebuilt;
  - an upload is three calls and a PUT to storage (multipart, five calls and
    a PUT per part), an operation must be polled, a list must be paged.

- **Decision.** Keep the generated clients as they are — the escape hatch
  for anything the layers do not cover — and put three layers over them, the
  same in Go and Python:

  1. **Every call** — `TokenSource` (`StaticToken`, `Session`), an
     idempotency key on every call whose `idempotency_level` is unknown (the
     rule `rpcmeta` states), retries with jitter that honour `Retry-After`
     within the deadline, a `User-Agent` naming the SDK.
  2. **The planes** — `Connect`/`connect` returns a client for every service
     of each plane, each sending its own audience's token. The per-plane
     types are *generated* from the descriptors, and a test fails when they
     are stale, so a service added to the contract cannot be left out.
  3. **What takes several calls** — `Pages`, `Wait`, `Mask`, `Upload`,
     `Download`. Few and hand-written; `Upload` follows the path the console
     already proved against real storage.

  What both SDKs and the server must agree on — header and audience names —
  is defined once in the Go SDK, imported by the server, and compared by a
  Python test.

- **Proposed, needing contract or server changes:**
  - `google.api.resource` annotations, and resource-name builders and
    parsers generated from them for both SDKs and the server;
  - `google.rpc.ErrorInfo` reasons on errors, so clients branch on a reason
    rather than a message;
  - a webhook signature over a timestamp and the body, with a verifier in
    both SDKs; today's signs the body alone, so a captured delivery replays;
  - an in-memory fake server for SDK users' tests, and one set of scenarios
    both SDKs run against the stack.

- **Consequences.** The SDK release is breaking: calls now carry keys they
  did not, a retry that cannot fit the deadline returns the server's error
  instead of the context's, and the Python `Client` grew parameters.
  Versioned with the contract, that is `0.12.0` — pre-1.0, a minor version
  may break. The MCP bridge can now drop its own interceptor and client set
  for the SDK's.
