## Unreleased — the product's numbering restarts at 0.x

- Product releases are tagged `release/vX.Y.Z` and restart at 0.x
  ([ADR-0045](../adr/0045-the-product-restarts-at-0x.md)); the images and
  charts carry the bare version, `0.1.0` and on. The old `vX.Y.Z` releases
  stay, and are never published again.
- 0.x sorts below every old release. A deployment that takes the highest
  chart or image version must bound it below 1.0 (for Helm,
  `>=0.0.0-0 <1.0.0-0`) or it stays on the old numbering.
- Before 1.0 a breaking change is a minor; read each release's *Behaviour
  changes* and its upgrade section, not the version, to tell whether it
  breaks anything.
