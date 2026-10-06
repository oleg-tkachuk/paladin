{{/*
Traffic alerts — otelconnect's `rpc.server.call.duration` histogram, rendered
by Prometheus as rpc_server_call_duration_seconds_{bucket,count}: `rpc_method`
names the RPC and `error_type` carries the upper-case Connect code of a failed
call. It covers every Connect listener, so the api and admin planes alike;
metrics.alerts.rpcSelector narrows it where the metrics pipeline adds labels
that tell them apart.
*/}}
{{- define "chart.alerts.traffic" -}}
{{- $rules := .Values.metrics.alerts.rules -}}
{{- $rpcCalls := "rpc_server_call_duration_seconds_count" -}}
{{- $rpcBuckets := "rpc_server_call_duration_seconds_bucket" -}}
{{- $rpcErrorLabel := "error_type" -}}
{{- $rpcSelector := .Values.metrics.alerts.rpcSelector -}}
{{- $rpcMatchers := ternary (printf "%s," $rpcSelector) "" (ne $rpcSelector "") -}}
{{- $rpcBraced := ternary (printf "{%s}" $rpcSelector) "" (ne $rpcSelector "") -}}
{{- if $rules.highErrorRate.enabled }}
{{- $errors := $rules.highErrorRate }}
- alert: PaladinApiHighErrorRate
  expr: |
    (
      sum(rate({{ $rpcCalls }}{ {{- $rpcMatchers -}} {{ $rpcErrorLabel }}=~"{{ join "|" $errors.serverErrorCodes }}"}[{{ $errors.window }}]))
      /
      sum(rate({{ $rpcCalls }}{{ $rpcBraced }}[{{ $errors.window }}]))
    ) > {{ $errors.threshold }}
  for: {{ $errors.for }}
  labels:
    severity: {{ $errors.severity }}
    service: paladin-api
  annotations:
    summary: "Paladin api error rate above {{ $errors.threshold | mulf 100.0 }}%"
    description: |
      More than {{ $errors.threshold | mulf 100.0 }}% of Connect RPCs over {{ $errors.window }} failed
      with a server-fault code ({{ join ", " $errors.serverErrorCodes }}) for {{ $errors.for }}.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $errors) | nindent 4 }}
{{- end }}
{{- if $rules.highLatency.enabled }}
{{- $latency := $rules.highLatency }}
- alert: PaladinApiHighLatency
  expr: |
    histogram_quantile(
      {{ $latency.quantile }},
      sum by (le) (rate({{ $rpcBuckets }}{{ $rpcBraced }}[{{ $latency.window }}]))
    ) > {{ $latency.thresholdSeconds }}
  for: {{ $latency.for }}
  labels:
    severity: {{ $latency.severity }}
    service: paladin-api
  annotations:
    summary: "Paladin api p{{ $latency.quantile | mulf 100.0 }} latency > {{ $latency.thresholdSeconds }}s"
    description: |
      p{{ $latency.quantile | mulf 100.0 }} Connect RPC latency over {{ $latency.window }} exceeded
      {{ $latency.thresholdSeconds }}s for {{ $latency.for }}.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $latency) | nindent 4 }}
{{- end }}
{{- end -}}
