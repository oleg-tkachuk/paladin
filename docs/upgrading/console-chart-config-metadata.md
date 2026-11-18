## release/v0.4.0 — the console chart drops `config` and `metadata`

- The console chart no longer takes `config` or `metadata`, and renders no
  `paladin-console-config` ConfigMap. Nothing read either since the console
  became a static export: the build metadata it shows is baked in at build
  time from the image's build args. The schema refuses unknown keys, so an
  overlay that still sets `config` or `metadata` fails to render: drop them.
- An overlay that replaced `volumes` or `volumeMounts` and kept the `config`
  volume must drop it too: it names the ConfigMap that is no longer rendered.
