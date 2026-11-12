{{/*
Expand the name of the chart.
*/}}
{{- define "chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name. Truncated at 63 chars per the
DNS naming spec. If the release name already contains the chart name we
collapse the duplication.
*/}}
{{- define "chart.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Per-role fullname. Each role (api / admin / worker / mcp) gets its own
Deployment / Service / ServiceAccount; the role suffix here keeps every
object distinct in `kubectl get` output and avoids conflicts when two
roles compute identical resource counts.

Caller passes a map: { "ctx": ., "role": "api" }.
*/}}
{{- define "chart.roleFullname" -}}
{{- $base := include "chart.fullname" .ctx -}}
{{- printf "%s-%s" $base .role | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels — chart-level, shared by every object the chart renders.
*/}}
{{- define "chart.labels" -}}
helm.sh/chart: {{ include "chart.chart" . }}
{{ include "chart.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels — chart-level, used by objects that target every pod the
chart owns regardless of role (e.g. NetworkPolicies that allow egress
from any plane).
*/}}
{{- define "chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Per-role labels and selectors — pin `app.kubernetes.io/component` so a
Service / HPA / NetworkPolicy can scope to one role only. The component
label is required for any Service to land traffic on the right pods.

Caller passes: { "ctx": ., "role": "api" }.
*/}}
{{- define "chart.roleLabels" -}}
{{ include "chart.labels" .ctx }}
app.kubernetes.io/component: {{ .role }}
{{- end -}}

{{- define "chart.roleSelectorLabels" -}}
{{ include "chart.selectorLabels" .ctx }}
app.kubernetes.io/component: {{ .role }}
{{- end -}}

{{/*
Chart-version label used by chart.labels.
*/}}
{{- define "chart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Service-account name resolution. Per-role `serviceAccount.name` overrides
the chart-level default; when neither is set the chart-fullname-suffixed
SA is used so every release gets a distinct identity.

Caller passes: { "ctx": ., "role": "api", "spec": <roleSpec> }.

The `spec.serviceAccount.name` override lets operators bind one role to a
custom SA (e.g. "paladin-mcp-runner" with extra KMS permissions) without
touching the others.
*/}}
{{- define "chart.roleServiceAccountName" -}}
{{- $ctx := .ctx -}}
{{- $role := .role -}}
{{- $spec := .spec -}}
{{- if and $spec.serviceAccount $spec.serviceAccount.name -}}
{{- $spec.serviceAccount.name -}}
{{- else if and $ctx.Values.serviceAccount.create (not $ctx.Values.serviceAccount.name) -}}
{{- include "chart.roleFullname" (dict "ctx" $ctx "role" $role) -}}
{{- else if $ctx.Values.serviceAccount.name -}}
{{- $ctx.Values.serviceAccount.name -}}
{{- else -}}
default
{{- end -}}
{{- end -}}

{{/*
chart.s3CredentialSecrets — the per-backend S3 credential Secrets, resolved.

Returns JSON: { "<backend>": { name, create, existingSecret, accessKey,
secretKey, active } }. Consumers do `fromJson (include
"chart.s3CredentialSecrets" .)` and iterate; `active` means "inject a
SecretRef for this backend and let the resolver GET this Secret".

Why a map at all. The injection used to be hardcoded to `primary`:

    {{- $primary := index $cfg.storage.backends "primary" }}

so a deployment with more than one backend had no way to keep the others'
credentials out of the ConfigMap — they could only be inlined, in plaintext,
readable by anyone who can GET a configmap in the namespace. The
CUE schema always allowed `access_key_secret` on every backend; only the
chart could not express it. One Secret per backend, so a credential can be
rotated, scoped or sourced (SOPS / ExternalSecret / SealedSecret) per backend
rather than all-or-nothing.

`storage.s3CredentialsSecret` (singular) is still honoured and means the
`primary` entry. Five overlays in this repo and in gitops set it, including
`create: false` to opt out; they keep working untouched. An explicit
`primary` key in the new map wins over it.

Default name: the singular carries `paladin-s3-credentials` in values.yaml and
keeps it, because renaming would orphan Secrets that already exist. Entries in
the map that name nothing get `<fullname>-s3-<backend>`.
*/}}
{{- define "chart.s3CredentialSecrets" -}}
{{- $out := dict -}}
{{- $ctx := . -}}
{{- with .Values.storage -}}
{{- $specs := dict -}}
{{- range $backend, $spec := (.s3CredentialsSecrets | default dict) -}}
{{- $_ := set $specs $backend $spec -}}
{{- end -}}
{{- if and .s3CredentialsSecret (not (hasKey $specs "primary")) -}}
{{- $_ := set $specs "primary" .s3CredentialsSecret -}}
{{- end -}}
{{- range $backend, $spec := $specs -}}
{{- $name := $spec.existingSecret | default $spec.name -}}
{{- if not $name -}}
{{- $name = printf "%s-s3-%s" (include "chart.fullname" $ctx) $backend -}}
{{- end -}}
{{- /* Booleans built with if, not `or … | ternary`: Go templates' `or`
       returns its last truthy OPERAND, so `or false "some-secret"` yields a
       string and ternary rejects it. That shape rendered fine wherever
       `create` was true and blew up only on the staging convention
       (create: false + existingSecret), which is exactly the combination
       this map has to support. */ -}}
{{- $active := false -}}
{{- if and $name (or $spec.create $spec.existingSecret) -}}
{{- $active = true -}}
{{- end -}}
{{- $create := false -}}
{{- if and $spec.create (not $spec.existingSecret) -}}
{{- $create = true -}}
{{- end -}}
{{- $_ := set $out $backend (dict
      "name" $name
      "create" $create
      "existingSecret" ($spec.existingSecret | default "")
      "accessKey" ($spec.accessKey | default "")
      "secretKey" ($spec.secretKey | default "")
      "active" $active) -}}
{{- end -}}
{{- end -}}
{{- toJson $out -}}
{{- end -}}

{{/*
chart.sessionKey — the session signing key's Secret (ADR-0047): the one this
chart generates, or auth.sessionKey.existingSecret. Mounted as a file into
auth.sessionKey.roles only. Returns JSON {active, create, name, key,
mountPath, roles}.
*/}}
{{- define "chart.sessionKey" -}}
{{- $session := .Values.auth.sessionKey -}}
{{- $name := $session.existingSecret | default (printf "%s-session-key" (include "chart.fullname" .)) -}}
{{- $active := or $session.generate (ne $session.existingSecret "") -}}
{{- $create := and $session.generate (eq $session.existingSecret "") -}}
{{- dict "active" $active "create" $create "name" $name "key" $session.key "mountPath" $session.mountPath "roles" $session.roles | toJson -}}
{{- end -}}

{{/*
chart.apiTokenHMACKey — where api_token.hmac_key comes from when API tokens
are on and config.api_token names neither an inline key nor a Secret: a
Secret this chart generates, or apiToken.hmacKey.existingSecret. It is the
digest pepper, a secret of its own rather than derived from the session key
(ADR-0047). Returns JSON {active, create, name, key}.
*/}}
{{- define "chart.apiTokenHMACKey" -}}
{{- $hk := .Values.apiToken.hmacKey -}}
{{- $cfg := .Values.config.api_token | default dict -}}
{{- $configured := false -}}
{{- if or $cfg.hmac_key (and $cfg.hmac_key_secret $cfg.hmac_key_secret.name) -}}
{{- $configured = true -}}
{{- end -}}
{{- $name := $hk.existingSecret | default (printf "%s-api-token-hmac" (include "chart.fullname" .)) -}}
{{- $active := false -}}
{{- if and $cfg.enabled (not $configured) (or $hk.generate $hk.existingSecret) -}}
{{- $active = true -}}
{{- end -}}
{{- $create := false -}}
{{- if and $active $hk.generate (not $hk.existingSecret) -}}
{{- $create = true -}}
{{- end -}}
{{- dict "active" $active "create" $create "name" $name "key" $hk.key | toJson -}}
{{- end -}}

{{/*
chart.refName — the Secret name a SecretRef gives: a bare string is the name,
a map its `name`; anything else none.
*/}}
{{- define "chart.refName" -}}
{{- if kindIs "string" . -}}{{ . }}{{- else if kindIs "map" . -}}{{ .name | default "" }}{{- end -}}
{{- end -}}

{{/*
chart.secretFamilies — the Secret names each of the resolver's binding paths
gives in this release (ADR-0047), keyed as backend/internal/config/roles.go
names them and files/role-secrets.json lists them per role. Built from the
same helpers the ConfigMap injects refs from, so the Role a role gets names
exactly the Secrets its config makes it read. Returns JSON {path: [names]}.
*/}}
{{- define "chart.secretFamilies" -}}
{{- $cfg := .Values.config -}}
{{- $pg := fromJson (include "chart.postgres" .) -}}
{{- $pgCfg := dig "datastores" "postgres" dict $cfg -}}
{{- $out := dict -}}
{{- $app := include "chart.refName" (dig "password_secret" "" $pgCfg) -}}
{{- $migrate := include "chart.refName" (dig "migrate_password_secret" "" $pgCfg) -}}
{{- if $pg.active -}}{{- $app = $pg.app.name -}}{{- $migrate = $pg.migrate.name -}}{{- end -}}
{{- $_ := set $out "postgres.password_secret" (list $app) -}}
{{- $_ := set $out "postgres.migrate_password_secret" (list $migrate) -}}
{{- $_ := set $out "postgres.reaper_password_secret" (list (include "chart.refName" (dig "reaper_password_secret" "" $pgCfg))) -}}
{{- $replica := list -}}
{{- if dig "replica" "enabled" false $pgCfg -}}{{- $replica = list (include "chart.refName" (dig "replica" "password_secret" "" $pgCfg)) -}}{{- end -}}
{{- $_ := set $out "postgres.replica.password_secret" $replica -}}
{{- $valkey := list -}}
{{- if eq (dig "middleware" "rate_limit" "backend" "" $cfg) "valkey" -}}{{- $valkey = list (include "chart.refName" (dig "datastores" "valkey" "password_secret" "" $cfg)) -}}{{- end -}}
{{- $_ := set $out "valkey.password_secret" $valkey -}}
{{- $boot := list -}}
{{- if dig "bootstrap" "admin" "enabled" false $cfg -}}{{- $boot = list (include "chart.refName" (dig "bootstrap" "admin" "password_secret" "" $cfg)) -}}{{- end -}}
{{- $_ := set $out "bootstrap.admin.password_secret" $boot -}}
{{- $pepper := fromJson (include "chart.apiTokenHMACKey" .) -}}
{{- $pepperName := include "chart.refName" (dig "api_token" "hmac_key_secret" "" $cfg) -}}
{{- if $pepper.active -}}{{- $pepperName = $pepper.name -}}{{- end -}}
{{- $_ := set $out "api_token.hmac_key_secret" (list $pepperName) -}}
{{- $_ := set $out "capability.dpop_nonce.key_secret" (list (include "chart.refName" (dig "capability" "dpop_nonce" "key_secret" "" $cfg))) -}}
{{- $_ := set $out "mcp.dpop_key_secret" (list (include "chart.refName" (dig "mcp" "dpop_key_secret" "" $cfg))) -}}
{{- $_ := set $out "runtime.health_snapshot_token_secret" (list (include "chart.refName" (dig "runtime" "health_snapshot_token_secret" "" $cfg))) -}}
{{- $_ := set $out "ingest.webhook.shared_secret_ref" (list (include "chart.refName" (dig "ingest" "webhook" "shared_secret_ref" "" $cfg))) -}}
{{- $_ := set $out "ingest.nats.token_ref" (list (include "chart.refName" (dig "ingest" "nats" "token_ref" "" $cfg))) -}}
{{- $_ := set $out "ingest.rabbitmq.url_ref" (list (include "chart.refName" (dig "ingest" "rabbitmq" "url_ref" "" $cfg))) -}}
{{- $_ := set $out "mcp.oauth.exchange.client_secret_ref" (list (include "chart.refName" (dig "mcp" "oauth" "exchange" "client_secret_ref" "" $cfg))) -}}
{{- $seeds := list -}}
{{- range $client := (dig "auth" "oauth" "seed_clients" list $cfg) -}}
{{- $seeds = append $seeds (include "chart.refName" (dig "client_secret_ref" "" $client)) -}}
{{- end -}}
{{- $_ := set $out "auth.oauth.seed_clients[" $seeds -}}
{{- $backends := list -}}
{{- range $backend, $sec := fromJson (include "chart.s3CredentialSecrets" .) -}}
{{- if $sec.active -}}{{- $backends = append $backends $sec.name -}}{{- end -}}
{{- end -}}
{{- range $name, $b := (dig "storage" "backends" dict $cfg) -}}
{{- range $field := list "access_key_secret" "secret_key_secret" "session_token_secret" -}}
{{- $backends = append $backends (include "chart.refName" (dig "auth" $field "" $b)) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $out "storage.backends." $backends -}}
{{- $out | toJson -}}
{{- end -}}

{{/*
chart.roleSecretNames — the Secrets role reads (ADR-0047): the names its
families in files/role-secrets.json give, plus rbac.secretReader.secretNames,
which every role keeps for a ref the chart cannot see. Returns a JSON list,
empty when the role reads none. Takes (dict "ctx" $ "role" <role>).
*/}}
{{- define "chart.roleSecretNames" -}}
{{- $table := .ctx.Files.Get "files/role-secrets.json" | fromJson -}}
{{- $families := fromJson (include "chart.secretFamilies" .ctx) -}}
{{- $names := .ctx.Values.rbac.secretReader.secretNames | default list -}}
{{- range $path := (index $table.roles .role | default list) -}}
{{- range $name := (index $families $path | default list) -}}
{{- if $name -}}{{- $names = append $names $name -}}{{- end -}}
{{- end -}}
{{- end -}}
{{- $names | uniq | sortAlpha | toJson -}}
{{- end -}}

{{/*
chart.postgres — the connection the chart builds from the top-level
`postgres` block. Returns JSON {active, dsn, migrateDsn, app, migrate}, where
app and migrate are password SecretRefs. Inactive when postgres.host is empty:
config.datastores.postgres is then used as written.
*/}}
{{- define "chart.postgres" -}}
{{- $pg := .Values.postgres | default dict -}}
{{- if $pg.host -}}
{{- $url := printf "%s:%v/%s?sslmode=%s" $pg.host $pg.port $pg.database $pg.sslmode -}}
{{- $ref := dict -}}
{{- $out := dict "active" true
      "dsn" (printf "postgres://%s@%s" $pg.app.user $url)
      "migrateDsn" (printf "postgres://%s@%s" $pg.migrate.user $url) -}}
{{- range $role := list "app" "migrate" -}}
{{- $spec := index $pg $role -}}
{{- $name := required (printf "postgres.%s.existingSecret is required when postgres.host is set: the Secret holding the %s role's password" $role $spec.user) $spec.existingSecret -}}
{{- $r := dict "name" $name "key" $spec.key -}}
{{- if $spec.namespace -}}{{- $_ := set $r "namespace" $spec.namespace -}}{{- end -}}
{{- $_ := set $out $role $r -}}
{{- end -}}
{{- $out | toJson -}}
{{- else -}}
{{- dict "active" false | toJson -}}
{{- end -}}
{{- end -}}

{{/*
chart.internalScheme — the scheme the planes speak inside the cluster: https
when internalTLS is on, http otherwise. Used by the MCP bridge's upstream URLs
(through tpl in values.yaml) and wherever else a plane is dialled.
*/}}
{{- define "chart.internalScheme" -}}
{{- if .Values.internalTLS.enabled -}}https{{- else -}}http{{- end -}}
{{- end -}}

{{/*
chart.internalTLSSecret — the Secret holding the planes' certificate, key and
CA. Required whenever internalTLS is on.
*/}}
{{- define "chart.internalTLSSecret" -}}
{{- required "internalTLS.existingSecret is required when internalTLS.enabled: a Secret with tls.crt, tls.key and ca.crt" .Values.internalTLS.existingSecret -}}
{{- end -}}

{{/*
chart.probe — a role's probe with its httpGet scheme set by internalTLS: HTTPS
for the roles whose listeners terminate TLS (internalTLS.serverRoles), as
written otherwise. Returns JSON.
*/}}
{{- define "chart.probe" -}}
{{- $probe := deepCopy .probe -}}
{{- $tls := .ctx.Values.internalTLS -}}
{{- if and $tls.enabled (has .role $tls.serverRoles) $probe.httpGet -}}
{{- $_ := set $probe.httpGet "scheme" "HTTPS" -}}
{{- end -}}
{{- $probe | toJson -}}
{{- end -}}

{{/*
Metrics — one contract from the process to the scraper (ADR-0023).
`metrics.mode` is the only switch: the exporter the application runs, the
container port, the PodMonitor and the NetworkPolicy scrape rule are all read
from these helpers, so no two of them can disagree.
*/}}

{{/* otel.metrics_exporter for each metrics.mode. */}}
{{- define "chart.metricsExporter" -}}
{{- $exporters := dict "push" "otlp" "scrape" "prometheus" "off" "none" -}}
{{- index $exporters .Values.metrics.mode -}}
{{- end -}}

{{/*
"true" when metrics are scraped, empty otherwise. Refuses scrape without
config.otel.enabled: the MeterProvider is off then, and the PodMonitor and
ports would point at a listener that never opens.
*/}}
{{- define "chart.metricsScraped" -}}
{{- if eq .Values.metrics.mode "scrape" -}}
{{- if not .Values.config.otel.enabled -}}
{{- fail "metrics.mode: scrape needs config.otel.enabled: true — without it no role opens the metrics port" -}}
{{- end -}}
true
{{- end -}}
{{- end -}}

{{/* The container port name the PodMonitor selects on. */}}
{{- define "chart.metricsPortName" -}}
metrics
{{- end -}}

{{/* The path the server mounts the exporter on (middleware.PathMetrics). */}}
{{- define "chart.metricsPath" -}}
/metrics
{{- end -}}

{{/* otel.metrics_addr: every interface, on metrics.port. */}}
{{- define "chart.metricsAddr" -}}
{{- printf "0.0.0.0:%d" (int .Values.metrics.port) -}}
{{- end -}}

{{/*
The roles that serve /metrics, as JSON. mcp is not one: it holds no
instruments of its own (serve_mcp.go).
*/}}
{{- define "chart.metricsRoles" -}}
{{- list "api" "iam" "admin" "worker" "dispatcher" "ingest" | toJson -}}
{{- end -}}

{{/*
The pod annotations a role carries for annotation-based discovery
(metrics.discovery: annotations), as JSON: prometheus.io/scrape, port and
path for a role that serves /metrics while metrics are scraped, {} otherwise.
Read by scrapers without Prometheus Operator — Alloy, vmagent, a Prometheus
kubernetes_sd job — from the same port and path the PodMonitor would use.

The chart owns the prometheus.io/* keys then, so a podAnnotations entry under
that prefix is refused rather than left to disagree with them.

Caller passes { "ctx": ., "role": "api", "podAnnotations": <the role's> }.
*/}}
{{- define "chart.metricsAnnotations" -}}
{{- $ctx := .ctx -}}
{{- $prefix := "prometheus.io/" -}}
{{- $out := dict -}}
{{- if and (include "chart.metricsScraped" $ctx) (eq $ctx.Values.metrics.discovery "annotations") -}}
{{- range $key, $_ := .podAnnotations -}}
{{- if hasPrefix $prefix $key -}}
{{- fail (printf "deployments.%s.podAnnotations sets %s, which metrics.discovery: annotations renders; drop it" $.role $key) -}}
{{- end -}}
{{- end -}}
{{- if has .role (include "chart.metricsRoles" $ctx | fromJsonArray) -}}
{{- $_ := set $out (printf "%sscrape" $prefix) "true" -}}
{{- $_ := set $out (printf "%sport" $prefix) (toString $ctx.Values.metrics.port) -}}
{{- $_ := set $out (printf "%spath" $prefix) (include "chart.metricsPath" $ctx) -}}
{{- end -}}
{{- end -}}
{{- toJson $out -}}
{{- end -}}
