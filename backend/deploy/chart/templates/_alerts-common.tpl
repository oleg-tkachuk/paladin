{{/*
chart.alerts.runbook — the runbook_url annotation for a rule, or nothing when
the rule names no runbook. Caller passes { "ctx": ., "rule": <rule values> }.
*/}}
{{- define "chart.alerts.runbook" -}}
{{- with .rule.runbook -}}
runbook_url: "{{ trimSuffix "/" $.ctx.Values.metrics.alerts.runbookBaseUrl }}/{{ . }}"
{{- end -}}
{{- end -}}
