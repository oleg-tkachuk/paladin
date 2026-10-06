# Configuration

The configuration reference lives in one place:
[docs/configuration.md](../../docs/configuration.md) — the load order (base
file, `PALADIN_CONFIG_OVERLAYS`, `PALADIN_` environment variables, CUE
defaults), the three validation gates, Secret resolution, and every top-level
block.

Every key, with its default and a comment, is in
[`configs/config.yaml`](../configs/config.yaml). The loader is
`internal/config`: `config.go` (`Load`, `Config.Validate`), `schema.cue`
(types and defaults), `strict.go` (unknown keys, `EnvKeyMapper`),
`resolver.go` (Kubernetes Secrets) and `weak_secrets.go`.
