## v40.2.0, sdk/go/v0.120.0, sdk/python/v0.121.0 — the configuration's defaults beside the running one

- **`GetConfigResponse` gains `defaults_yaml`**: the configuration a
  deployment runs on where it sets nothing, in the running one's shape and
  redaction. Additive.
- The console's `/config` Compare opens on the defaults, so an operator sees
  every setting the deployment changed; a pasted `config.yaml` is one click
  away.
