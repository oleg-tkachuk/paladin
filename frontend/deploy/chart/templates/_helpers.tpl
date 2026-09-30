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
chart.backendURL — one backend plane's URL: backend.urls.<plane>
when set, otherwise built from the backend release. Called with
(dict "ctx" $ "plane" "<plane>").

The ports are the backend chart's Service ports; its worker and dispatcher
Services map :8099 onto container ports of their own.
*/}}
{{- define "chart.backendURL" -}}
{{- $b := .ctx.Values.backend -}}
{{- $explicit := index $b.urls .plane -}}
{{- if $explicit -}}
{{- $explicit -}}
{{- else -}}
{{- $services := dict
      "data" (list "api" 8080 true)
      "iam" (list "api" 8085 true)
      "admin" (list "admin" 8090 true)
      "worker" (list "worker" 8099 false)
      "mcp" (list "mcp" 8095 false)
      "dispatcher" (list "dispatcher" 8099 false)
      "ingest" (list "ingest" 8100 false) -}}
{{- $svc := index $services .plane -}}
{{- $fullname := $b.release -}}
{{- if not (contains $b.chart $b.release) -}}
{{- $fullname = printf "%s-%s" $b.release $b.chart -}}
{{- end -}}
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
