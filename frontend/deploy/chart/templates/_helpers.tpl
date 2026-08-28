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
Refuse to render more than one console replica.

The BFF keeps two maps in PROCESS memory (src/lib/auth/bff.ts): the dedup that
collapses concurrent refresh-token rotations, and the successor index that lets
an ExchangeAudience follow a rotation which consumed its token. With one
replica both are exactly right. With two, a request routed to the replica that
did not perform the rotation finds nothing to follow, presents a spent token,
and the backend does what it should — refuses it, and the operator is signed
out by a race they cannot see or avoid.

That failure is invisible at deploy time and intermittent at run time, which is
the worst combination to debug. So the constraint is enforced here rather than
written in a comment nobody reads while scaling up during an incident.

Lift it by moving the bookkeeping to a shared store (Redis), not by deleting
this check — BACKLOG carries that item.
*/}}
{{- define "chart.assertSingleReplica" -}}
{{- if gt (int .Values.replicaCount) 1 -}}
{{- fail (printf "paladin-console: replicaCount=%d, but the BFF's token-rotation bookkeeping is per-process — a second replica signs operators out at random. See src/lib/auth/bff.ts and BACKLOG \"The BFF's rotation bookkeeping is per-process\". Move it to a shared store before scaling." (int .Values.replicaCount)) -}}
{{- end -}}
{{- if .Values.autoscaling.enabled -}}
{{- if gt (int .Values.autoscaling.maxReplicas) 1 -}}
{{- fail (printf "paladin-console: autoscaling would reach %d replicas, but the BFF's token-rotation bookkeeping is per-process — see src/lib/auth/bff.ts. Autoscaling this component is not safe until that state is shared." (int .Values.autoscaling.maxReplicas)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
