{{/*
Expand the name of the chart.
*/}}
{{- define "chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
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
Create chart name and version as used by the chart label.
*/}}
{{- define "chart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
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
Selector labels
*/}}
{{- define "chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "chart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "chart.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}


{{/*
chart.backendFullname — the backend release's fullname, which its Services
are named after: the release name, suffixed with the chart name unless the
release name already contains it.
*/}}
{{- define "chart.backendFullname" -}}
{{- $b := .Values.backend -}}
{{- if contains $b.chart $b.release -}}
{{- $b.release -}}
{{- else -}}
{{- printf "%s-%s" $b.release $b.chart -}}
{{- end -}}
{{- end -}}

{{/*
chart.sessionService — the Service the console's /session/ paths go to: the
backend release's session role, unless session.service.name names another.
*/}}
{{- define "chart.sessionService" -}}
{{- .Values.session.service.name | default (printf "%s-session" (include "chart.backendFullname" .)) -}}
{{- end -}}

{{/*
chart.backendURL — one backend plane's URL: backend.urls.<plane>
when set, otherwise built from the backend release. Called with
(dict "ctx" $ "plane" "<plane>"). The console's server reaches only iam.
*/}}
{{- define "chart.backendURL" -}}
{{- $b := .ctx.Values.backend -}}
{{- $explicit := index $b.urls .plane -}}
{{- if $explicit -}}
{{- $explicit -}}
{{- else -}}
{{- $services := dict "iam" (list "iam" 8085 true) -}}
{{- $svc := index $services .plane -}}
{{- $fullname := include "chart.backendFullname" .ctx -}}
{{- $scheme := "http" -}}
{{- if and $b.tls (index $svc 2) -}}{{- $scheme = "https" -}}{{- end -}}
{{- $ns := $b.namespace | default .ctx.Release.Namespace -}}
{{- printf "%s://%s-%s.%s.svc.cluster.local:%v" $scheme $fullname (index $svc 0) $ns (index $svc 1) -}}
{{- end -}}
{{- end -}}

{{/*
chart.backendCAPath — the mount path of the backend CA, when backend.tls
is on. Fails without a caSecret: Node rejects the internal CA otherwise.
*/}}
{{- define "chart.backendCAPath" -}}
/etc/paladin-backend-ca
{{- end -}}

