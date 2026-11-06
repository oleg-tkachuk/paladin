# ADR-0043: Stabilize how the SDKs are delivered

- **Status:** Accepted 2026-11-06 (steps 1–6 implemented; step 7 waits for limes 1.0).
- **Related:** [ADR-0018](0018-sdk-layers.md) (the layers),
  [ADR-0020](0020-sdk-integration-grade.md) (integration grade).

- **Context.** The SDKs' design holds: both implement ADR-0018's three
  layers, the plane facades are generated and checked for staleness, errors
  are typed from the contract, and parity is tested against shared fixtures
  and scenarios. What a consumer finds hard is delivery, not design:

  - **Churn.** 162 `sdk/go/*` tags across 127 minor versions in the five
    weeks since 2026-10-02; pre-1.0, any minor may break.
  - **A pre-release floor.** Every Go SDK release raises each consumer's
    `limes` requirement to the newest release candidate, which may itself
    break. The backend builds the SDK through `replace => ../sdk/go`, so its
    `require` of `sdk/go` is never resolved and nothing in CI sees the module
    the way a consumer does.
  - **One package.** `sdk/go/paladin` links biscuit-go, go-spiffe and twelve
    limes packages for every caller, though only Biscuit attenuation, SPIFFE
    TLS and DPoP use them. No test bounds what it imports.
  - **Test-only requirements.** OpenTelemetry is required by `sdk/go/go.mod`
    for one test, so it raises consumers' OpenTelemetry floor, against
    ADR-0020's "no observability dependency".
  - **Python.** The SDK is installed from git, ships `py.typed` with no type
    checker in CI and `Any` on load-bearing parameters, and declares Python
    3.14 and 3.15 while its `biscuit` extra installs only on 3.13.

- **Decision.** Keep the design; change how it is delivered, in steps that
  each land on their own:

  1. **No test-only requirement in `sdk/go/go.mod`.** A test needing a
     dependency consumers do not need lives in a module of its own. A test in
     `sdk/go` fails when the module requires, or `paladin` links,
     OpenTelemetry.
  2. **The heavy features stay in `paladin`.** Splitting Biscuit
     attenuation, SPIFFE TLS and DPoP into sub-packages was measured on a
     minimal client (bearer token, one `Health.GetVersion` call) on
     2026-11-06 and does not pay:
     - Biscuit, SPIFFE and limes code is about 70 KB of an 11.4 MB stripped
       binary (about 0.6%); the linker drops what the client does not call.
     - Their `init` costs about 0.1 ms of 1.1 ms.
     - They are 21 of the 99 third-party packages linked.
     - Sub-packages of the same module leave a consumer's module graph
       unchanged; only separate modules would shrink it, at the cost of more
       release streams, the churn this ADR set out to reduce.
  3. **CI resolves the published module.** A consumer check builds against
     `sdk/go` as a dependency, without `replace`, so a requirement the backend
     never sees fails there.
  4. **Both SDKs release at one version.** Decided 2026-11-06: one stream
     for both, cut from the commits that touch either SDK, `proto/` or
     `sdk/testdata/`, tagged `sdk/python/vX.Y.Z` by semantic-release and
     `sdk/go/vX.Y.Z` beside it on the same commit, with one GitHub release.
     A consumer of both reads one number; the cost is that a change to one
     SDK releases the other unchanged, which the release notes make plain.
     The Python tags are the stream's, being ahead when the two were joined
     (`sdk/python/v0.138.3` against `sdk/go/v0.137.13`), so no version it cuts
     already exists on either side.
  5. **Python is type-checked.** A strict type checker runs in CI over `src`,
     and the public parameters typed `Any` get their real types.
  6. **The `biscuit` extra states where it installs.** It carries a
     `python_version < '3.14'` marker until `biscuit-python` ships a newer
     wheel, and the README's compatibility table says so.
  7. **1.0 follows limes 1.0.** Once limes is stable, the SDKs cut 1.0 under
     semver, and the Python SDK is published to PyPI with the same release.

- **Alternatives.** Leave delivery as it is: every limes bump keeps reaching
  consumers as a breaking floor, and the Python SDK stays git-only. A thinner
  SDK — dropping the in-memory fakes and the multi-call layer — would cut the
  most code, but those are what consumers came for and what parity is tested
  through.

- **Consequences.** After 1.0 a breaking contract change costs a
  major version, which slows changes the dev cluster otherwise takes freely;
  tying 1.0 to limes 1.0 rather than a date keeps that cost until the base
  under it stops moving. Each step is tracked in `BACKLOG.md` until it lands.
