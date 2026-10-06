{{/*
Lifecycle alerts — one rule per alert, fanned out per role by a `component`
label taken from the pod name. Pods are named after their Deployment,
<fullname>-<role>-<replicaset>-<pod>, so the role is read from kube-state-
metrics' own `pod` label: no kube_pod_labels join, which exists only where
kube-state-metrics is told to export pod labels. The hook Jobs' pods
(<fullname>-migrate-…) match no role and are left out.
*/}}
{{- define "chart.alerts.lifecycle" -}}
{{- $rules := .Values.metrics.alerts.rules -}}
{{- $roles := list -}}
{{- range $role, $spec := .Values.deployments -}}
{{- if $spec.enabled }}{{ $roles = append $roles $role }}{{ end -}}
{{- end -}}
{{- $podPattern := printf "%s-(%s)-.+" (include "chart.fullname" . | regexQuoteMeta) (join "|" $roles) -}}
{{- $ns := .Release.Namespace -}}
{{- $container := .Chart.Name -}}
{{- if $rules.crashLooping.enabled }}
{{- $crash := $rules.crashLooping }}
- alert: PaladinCrashLooping
  expr: |
    label_replace(
      increase(kube_pod_container_status_restarts_total{namespace="{{ $ns }}",container="{{ $container }}",pod=~"{{ $podPattern }}"}[{{ $crash.window }}]) > {{ $crash.restarts }},
      "component", "$1", "pod", "{{ $podPattern }}"
    )
  for: {{ $crash.for }}
  labels:
    severity: {{ $crash.severity }}
  annotations:
    summary: "Paladin {{ "{{ $labels.component }}" }} pod is crash-looping"
    description: |
      Pod {{ "{{ $labels.pod }}" }} in namespace {{ $ns }}
      has restarted more than {{ $crash.restarts }} times in {{ $crash.window }}. Likely panic on startup,
      failed Postgres dial, or OOM. Inspect:
        kubectl -n {{ $ns }} logs {{ "{{ $labels.pod }}" }} --previous
{{- end }}
{{- if $rules.notReady.enabled }}
- alert: PaladinNotReady
  expr: |
    label_replace(
      kube_pod_status_ready{namespace="{{ $ns }}",condition="true",pod=~"{{ $podPattern }}"} == 0,
      "component", "$1", "pod", "{{ $podPattern }}"
    )
  for: {{ $rules.notReady.for }}
  labels:
    severity: {{ $rules.notReady.severity }}
  annotations:
    summary: "Paladin {{ "{{ $labels.component }}" }} pod stuck NotReady"
    description: |
      Pod {{ "{{ $labels.pod }}" }} has been NotReady for {{ $rules.notReady.for }}.
      Readiness probe failing — check /readyz on the {{ "{{ $labels.component }}" }} pod.
{{- end }}
{{- if $rules.oomKilled.enabled }}
- alert: PaladinOOMKilled
  expr: |
    label_replace(
      increase(kube_pod_container_status_last_terminated_reason{namespace="{{ $ns }}",container="{{ $container }}",reason="OOMKilled",pod=~"{{ $podPattern }}"}[{{ $rules.oomKilled.window }}]) > 0,
      "component", "$1", "pod", "{{ $podPattern }}"
    )
  labels:
    severity: {{ $rules.oomKilled.severity }}
  annotations:
    summary: "Paladin {{ "{{ $labels.component }}" }} was OOMKilled"
    description: |
      Pod {{ "{{ $labels.pod }}" }} was OOMKilled in the last {{ $rules.oomKilled.window }}.
      Bump deployments.{{ "{{ $labels.component }}" }}.resources.limits.memory in the env overlay,
      or chase the leak.
{{- end }}
{{- end -}}
