{{/*
Operations alerts — the work that does not show up in RPC metrics: the
outbox, rate limiting, capability charges, the read replica and uploads the
reconciler cannot settle.

Several of these counters exist only after the path they count has run once.
A rule on an absent series does not fire, so the counter rules use
`or vector(0)` where an absent series means "zero", and none of them alert
on absence.
*/}}
{{- define "chart.alerts.operations" -}}
{{- $rules := .Values.metrics.alerts.rules -}}
{{- if $rules.outboxNotDraining.enabled }}
{{- $outbox := $rules.outboxNotDraining }}
{{- /* The floor over the window, not the depth: depth spikes on every write
       burst, the minimum does not. Subscribers fall behind while RPCs keep
       succeeding (ADR-0003). */}}
- alert: PaladinOutboxNotDraining
  expr: |
    min_over_time(max(paladin_outbox_pending)[{{ $outbox.window }}:{{ $outbox.step }}]) > {{ $outbox.rows }}
  for: {{ $outbox.for }}
  labels:
    severity: {{ $outbox.severity }}
  annotations:
    summary: "Paladin outbox has not drained below {{ $outbox.rows }} rows in {{ $outbox.window }}"
    description: >-
      The dispatcher is not clearing pending outbox rows. Check the
      dispatcher logs, and the deepest-tenant line on the Operations
      dashboard to tell a dispatcher problem from one tenant's sink.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $outbox) | nindent 4 }}
{{- end }}
{{- if $rules.rateLimitFailingOpen.enabled }}
{{- $failOpen := $rules.rateLimitFailingOpen }}
{{- /* Admitting on a storage failure is intended, but the ceiling is not
       enforced while it lasts, and nothing else reports it. */}}
- alert: PaladinRateLimitFailingOpen
  expr: |
    (sum(increase(paladin_tenant_ratelimit_fail_open_total[{{ $failOpen.window }}])) or vector(0))
      + (sum(increase(paladin_api_token_ratelimit_fail_open_total[{{ $failOpen.window }}])) or vector(0))
      > 0
  for: {{ $failOpen.for }}
  labels:
    severity: {{ $failOpen.severity }}
  annotations:
    summary: "Paladin rate limiting is failing open"
    description: >-
      Requests are admitted without a rate-limit decision; the per-tenant
      or per-token ceiling is not in force. Check the limiter's Postgres
      tables and the api logs.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $failOpen) | nindent 4 }}
{{- end }}
{{- if $rules.capabilityChargesRefused.enabled }}
{{- $charges := $rules.capabilityChargesRefused }}
{{- /* Occasional refusals are a cap working; a tenant refused nearly every
       time cannot work, and from outside looks like an idle tenant. */}}
- alert: PaladinCapabilityChargesRefused
  expr: |
    sum by (tenant_id) (rate(paladin_capability_charges_total{outcome=~"{{ join "|" $charges.refusedOutcomes }}"}[{{ $charges.window }}]))
      /
    clamp_min(sum by (tenant_id) (rate(paladin_capability_charges_total[{{ $charges.window }}])), {{ $charges.minRate }})
      > {{ $charges.refusedFraction }}
  for: {{ $charges.for }}
  labels:
    severity: {{ $charges.severity }}
  annotations:
    summary: "Tenant {{ "{{ $labels.tenant_id }}" }} has nearly every capability charge refused"
    description: >-
      Over {{ $charges.refusedFraction | mulf 100.0 }}% of charge attempts were refused for {{ $charges.for }}. Check the
      tenant's quota and the issuing capability's budget.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $charges) | nindent 4 }}
{{- end }}
{{- if $rules.tenantThrottled.enabled }}
{{- $throttled := $rules.tenantThrottled }}
{{- /* A client in a loop, or a limit below the tenant's real workload; the
       tenant receives ResourceExhausted either way. */}}
- alert: PaladinTenantThrottled
  expr: |
    sum by (tenant_id) (rate(paladin_tenant_ratelimit_decisions_total{allowed="false"}[{{ $throttled.window }}])) > {{ $throttled.refusalsPerSecond }}
  for: {{ $throttled.for }}
  labels:
    severity: {{ $throttled.severity }}
  annotations:
    summary: "Tenant {{ "{{ $labels.tenant_id }}" }} is rate-limited steadily"
    description: >-
      The per-tenant limiter has refused more than {{ $throttled.refusalsPerSecond }} request/s for {{ $throttled.for }}.
      Either a client is looping or middleware.rate_limit is too low for
      this tenant.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $throttled) | nindent 4 }}
{{- end }}
{{- if $rules.readReplicaOutOfSync.enabled }}
{{- $replica := $rules.readReplicaOutOfSync }}
{{- /* Nothing fails while it lasts — reads fall back to the primary — which is
       why it needs an alert. The series exists only where the replica is
       enabled. */}}
- alert: PaladinReadReplicaOutOfSync
  expr: |
    max(paladin_db_replica_in_sync) == 0
  for: {{ $replica.for }}
  labels:
    severity: {{ $replica.severity }}
  annotations:
    summary: "Paladin read replica has served no reads for {{ $replica.for }}"
    description: >-
      Every pod is sending listing reads to the primary. The health page's
      postgres-replica row says why (unreachable, lag, not probed);
      paladin_db_replica_lag_seconds shows how far behind it is.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $replica) | nindent 4 }}
{{- end }}
{{- if $rules.uploadsNotSettling.enabled }}
{{- $uploads := $rules.uploadsNotSettling }}
{{- /* PENDING past its presign expiry plus the reconciler's min_object_age,
       and waiting longer still. reconcile() logs a failed HEAD or promote and
       the tick still succeeds, so the worker alerts stay quiet while it
       happens. A reconciler not running at all is PaladinWorkerStalled's. */}}
- alert: PaladinUploadsNotSettling
  expr: |
    max(paladin_objects_pending_overdue_age_seconds) > {{ $uploads.overdueSeconds }}
  for: {{ $uploads.for }}
  labels:
    severity: {{ $uploads.severity }}
  annotations:
    summary: "Paladin has uploads the reconciler cannot settle"
    description: >-
      The oldest overdue PENDING object is {{ "{{ $value | humanizeDuration }}" }}
      past the reconciler's deadline. Those objects cannot be downloaded
      until it completes or fails them. Check the worker logs for failed
      HEADs and paladin_objects_pending_overdue for how many are waiting.
    {{- include "chart.alerts.runbook" (dict "ctx" . "rule" $uploads) | nindent 4 }}
{{- end }}
{{- end -}}
