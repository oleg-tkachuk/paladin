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
