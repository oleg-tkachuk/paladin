# ADR-0042: An error about a presented capability is answered by its limes code

- **Status:** Accepted. Implemented 2026-10-29.
- **Related:** limes' ADR-0012 (a stable code for every error);
  [ADR-0010](0010-capability-as-establishing-credential.md).

- **Context.** Verifying a presented capability and metering it answer one
  caller about one capability, but each mapped limes' errors to connect
  codes its own way: verification by refusal reason, metering by a chain of
  `errors.Is`. They disagreed — a spent copy limit was `PERMISSION_DENIED`
  when verified and `RESOURCE_EXHAUSTED` when metered — and both answered
  the server's own faults (`invalid_config`, `internal`) as `UNAVAILABLE`,
  which clients retry. Metering also told the caller the store's error text.
  limes names every error with a code and lists them (`limes.Codes`), and
  leaves mapping them onto a transport to the consumer, in one place.

- **Decision.**
  1. One table, `auth.capabilityCodes`, maps every limes and meter code to a
     connect code, and one function, `auth.capabilityFailure`, answers every
     error about a presented capability through it, verified or metered.
  2. A test holds the table to `limes.Codes` and `meter.Codes`; a code with
     no row, or an error with no code, answers `UNAVAILABLE`.
  3. `UNAVAILABLE` is kept for a failing dependency; the server's own fault
     is `INTERNAL`; a refusal of the capability is `PERMISSION_DENIED` with
     its reason, and a spent limit `RESOURCE_EXHAUSTED`. Server-side
     failures are logged with their cause, and the caller is told a fixed
     sentence unless the error is a refusal.
  4. The text of a limes error is not mapped on: the code is the contract.

- **Consequences.**
  - A spent copy limit, refused while verifying, is `RESOURCE_EXHAUSTED`, as
    the SDKs' test servers already answered it.
  - A limes release that adds a code fails the test until the code is given
    an answer.
  - The admin API answers limes errors through the same table, via
    `auth.AnswerCapabilityError`, with each operation's exceptions as data:
    the same code can mean something else per operation (`revoked` while
    delegating is the parent's state, `FAILED_PRECONDITION`; a record the
    request names is `NOT_FOUND`). A test holds every exception row to
    codes limes defines. A store failing there is `UNAVAILABLE`, told only
    to retry; its text is logged as the failure's cause.
