# ADR-0045: The product restarts at 0.x, and a major is cut by hand

- **Status:** Accepted 2026-11-08.
- **Related:** [ADR-0034](0034-the-product-releases-as-a-train.md) (the
  release train; its "a train of breaking changes is one major" no longer
  holds before 1.0), [ADR-0043](0043-sdk-delivery-stabilization.md) (the
  SDKs, pre-1.0 already).

## Context

The product was tagged `vX.Y.Z` from 1.0.0, and every breaking commit made
the next release a major. Paladin changes its API and configuration while it
is built, and a breaking change landed most weeks, so it reached v42 within a
year. A major number that moves weekly says nothing a deployment can use: it
neither marks a compatibility promise nor tells one release line from another.

semantic-release reads the last release from the highest tag of its format.
Under `v`, a 0.x tag would always be lower than v42, so the numbering cannot
restart under the same prefix; the release-tag ruleset forbids deleting the
old tags, and deleting published releases would rewrite public history.

## Decision

- The product's tags become `release/vX.Y.Z`, starting at 0.x. The stream's
  first tag, `release/v0.0.0`, is cut by hand, as an SDK stream's first was;
  until it exists the workflow releases no product.
- Before 1.0, a breaking commit is a minor, as it already is for the SDKs.
- A release can be raised by hand: `release.yaml`'s `bump` input asks for at
  least a minor or a major whatever the commits say. 1.0.0 is cut that way.
- From 1.0, the pre-1.0 rule is dropped from the product stream: a major then
  comes from a breaking commit or from the `bump` input, and nothing else.
- The old `vX.Y.Z` tags stay. They still name their sections of the upgrade
  guide, and are never cut or published again.

## Consequences

- Version numbers say something again: 0.x is a product whose contract still
  moves, and 1.0 is a promise made on purpose.
- A breaking change no longer shows in the number before 1.0; its commit's
  `!` still lists it first in the release notes and its `docs/upgrading/`
  section, which is what an operator reads.
- 0.x sorts below 42.x. Whatever resolves the product's charts or images by
  the highest version must be bounded below 1.0 until 1.0 is cut — the dev
  cluster's chart constraint among them — or it keeps the old numbering.
- The task library derives dev builds' versions from the last tag of the
  product's prefix. With none it starts them at `0.1.0-dev.…`, which would
  outrank the `0.0.1-dev.…` builds that follow `release/v0.0.0`; so that tag
  is cut on main before the prefix reaches it, and dev builds restart at
  `0.0.1-dev.…`.
