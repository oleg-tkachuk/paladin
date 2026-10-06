{{/*
Worker alerts — the paladin_worker_* series every periodic worker records
through internal/worker.RunTicker. Both rules carry the `worker` label, so one
rule fans out into one alert per worker with no per-worker threshold: the
stall threshold is a multiple of the worker's own configured interval.
*/}}
{{- define "chart.alerts.workers" -}}
{{- $rules := .Values.metrics.alerts.rules -}}
{{- if $rules.workerStalled.enabled }}
{{- $stalled := $rules.workerStalled }}
{{- /* The series exist only once a worker has run, so a fresh boot does not
       fire; `for` rides out one slow tick. */}}
- alert: PaladinWorkerStalled
  expr: |
    (time() - paladin_worker_last_run_timestamp_seconds)
      > {{ $stalled.intervals }} * paladin_worker_interval_seconds
  for: {{ $stalled.for }}
  labels:
    severity: {{ $stalled.severity }}
  annotations:
    summary: "Paladin worker {{ "{{ $labels.worker }}" }} stalled"
    description: >-
      Worker {{ "{{ $labels.worker }}" }} has not completed a tick in over {{ $stalled.intervals }}× its
      configured interval ({{ "{{ $value | humanizeDuration }}" }} since the last
      run). Background maintenance it owns (reconciliation, purges,
      reaping, lifecycle, replication) is not running.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $stalled) | nindent 4 }}
{{- end }}
{{- if $rules.workerTicksAllFailing.enabled }}
{{- $failing := $rules.workerTicksAllFailing }}
{{- /* Running but wedged on a persistent failure, distinct from not running
       at all above. */}}
- alert: PaladinWorkerTicksAllFailing
  expr: |
    sum by (worker) (increase(paladin_worker_runs_total{outcome="error"}[{{ $failing.window }}])) > 0
    unless
    sum by (worker) (increase(paladin_worker_runs_total{outcome="success"}[{{ $failing.window }}])) > 0
  for: {{ $failing.for }}
  labels:
    severity: {{ $failing.severity }}
  annotations:
    summary: "Paladin worker {{ "{{ $labels.worker }}" }} failing every tick"
    description: >-
      Worker {{ "{{ $labels.worker }}" }} has logged only error outcomes and no
      successes in the last {{ $failing.window }}. The loop is alive but every tick is
      failing.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $failing) | nindent 4 }}
{{- end }}
{{- end -}}
