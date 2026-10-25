# ADR-0039: Paladin keeps its name, qualified wherever it is published

- **Status:** Accepted 2026-10-26.
- **Related:** closes the BACKLOG entry "The `paladin` name collides —
  decide qualify-vs-rename before publishing"; the trademark question it
  raised stays in [BACKLOG.md](../../BACKLOG.md).

- **Context.** "Paladin" is a common word, and registries hold it bare.
  Checked 2026-10-26:
  - **PyPI `paladin`** is an Android dependency manager, eight releases in
    June 2014 and none since, a single sdist. The NVIDIA project the
    2026-08-21 check found there (`nv-paladin`) no longer answers: the
    GitHub organisation is gone.
  - **npm `paladin`** is v0.1.1, last modified 2022. The `@paladin` scope is
    unclaimed.
  - **PyPI `paladin-sdk`** is free.
  - Other 2026 packages use the word (`paladin-mcp`, `paladin-verify`,
    `@momidala/paladin`), uncoordinated.

  What Paladin publishes today is already named apart from the bare word:

  | Artifact | Name |
  |---|---|
  | Python SDK distribution | `paladin-sdk` (not yet on PyPI) |
  | Go modules | `github.com/oleg-tkachuk/paladin/backend`, `…/sdk/go` |
  | Images | `ghcr.io/oleg-tkachuk/paladin-core`, `…/paladin-console` |
  | Helm charts | `oci://ghcr.io/oleg-tkachuk/charts/paladin-core`, `…/paladin-console` |

- **Decision.**
  1. **The product keeps the name Paladin.** No rename.
  2. **Everything published carries a qualified name**: the owner's
     namespace (Go module path, GHCR), or a suffix where a registry has no
     namespace (`paladin-sdk` on PyPI). A bare `paladin` is never published.
  3. **JavaScript, if it is ever published, goes under a scope** — `@paladin`
     while it stays free — never the bare name.
  4. **The Python import package stays `paladin`.** The generated modules
     live at their proto package's path (`paladin.admin.v1`, …), and that
     package is in every Connect route (`/paladin.admin.v1.…`), so renaming
     the import means changing the wire. A clash needs the abandoned 2014
     sdist installed beside the SDK.

- **Consequences.**
  - Nothing in the tree is renamed; the decision is what to call the next
    artifact, and a release that publishes one checks it against this ADR.
  - The first PyPI release claims `paladin-sdk`; until then the name is only
    free, not held.
  - The SDK wheel also installs a top-level `buf` package
    (`buf.validate`'s generated code). Another wheel shipping the same
    modules would overwrite them; none of the SDK's dependencies does.
  - Trademark use is not answered by this; it applies whether or not anything
    is published.
