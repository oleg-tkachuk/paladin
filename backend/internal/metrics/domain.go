package metrics

import (
	"context"

	"connectrpc.com/connect"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Domain instruments for the operations Paladin exists to perform.
//
// otelconnect already gives RED for every RPC — rate, errors, duration by
// method. That answers "is the service up" but not "is it doing its job":
// a presign that consistently returns PermissionDenied is a healthy-looking
// RPC and a broken product, and a budget that rejects every charge shows up
// as ResourceExhausted with no way to tell an exhausted tenant from a
// misconfigured one.
//
// So these count the decisions, not the calls. Each is deliberately
// low-cardinality: outcomes are bounded enums and tenant_id is the finest
// dimension, for the reason the api_token instruments already document —
// per-object or per-capability labels blow the exporter.
var (
	meterDomain = otel.Meter("github.com/oleg-tkachuk/paladin/internal/metrics/domain")

	presignIssued  metric.Int64Counter
	presignSeconds metric.Float64Histogram

	capabilityCharges metric.Int64Counter
	capabilityAmount  metric.Float64Histogram

	objectLockOps metric.Int64Counter

	// The four below answer "why", where otelconnect's RED answers "what".
	// Each was picked because the RPC-level signal shows the symptom and hides
	// the cause: a degrading object store looks like slow RPCs, a quota wall
	// looks like ResourceExhausted with no way to tell an exhausted tenant from
	// a misconfigured cap, a credential-stuffing run looks like ordinary Login
	// traffic, and an idempotency cache that stopped replaying looks like
	// nothing at all until a duplicate side effect turns up.
	storageCalls   metric.Int64Counter
	storageSeconds metric.Float64Histogram

	quotaDecisions metric.Int64Counter

	loginAttempts metric.Int64Counter

	idempotencyLookups metric.Int64Counter
)

func init() {
	var err error
	if presignIssued, err = meterDomain.Int64Counter(
		"paladin_presign_total",
		metric.WithDescription(
			"Presign requests by operation (get|put|post|part) and outcome. Paladin never "+
				"proxies bytes, so this is the closest thing to a throughput signal "+
				"the control plane has."),
	); err != nil {
		otel.Handle(err)
	}
	if presignSeconds, err = meterDomain.Float64Histogram(
		"paladin_presign_duration_seconds",
		metric.WithDescription("Presign wall-clock duration, including bucket resolution and the policy decision."),
		metric.WithUnit("s"),
	); err != nil {
		otel.Handle(err)
	}
	if capabilityCharges, err = meterDomain.Int64Counter(
		"paladin_capability_charges_total",
		metric.WithDescription(
			"Capability charge attempts by outcome (charged|capability_exhausted|"+
				"tenant_exhausted|error). A tenant whose charges are all rejected "+
				"cannot work, and looks identical to an idle one without this."),
	); err != nil {
		otel.Handle(err)
	}
	if capabilityAmount, err = meterDomain.Float64Histogram(
		"paladin_capability_charge_amount",
		metric.WithDescription("Amount per successful charge, in the capability's unit."),
		metric.WithUnit("1"),
	); err != nil {
		otel.Handle(err)
	}
	if objectLockOps, err = meterDomain.Int64Counter(
		"paladin_object_lock_operations_total",
		metric.WithDescription(
			"Object-lock writes by operation (retention|legal_hold), mode and outcome. "+
				"A COMPLIANCE retention cannot be shortened by anyone, so each one is "+
				"an irreversible commitment of storage — worth counting on its own."),
	); err != nil {
		otel.Handle(err)
	}
	if storageCalls, err = meterDomain.Int64Counter(
		"paladin_storage_calls_total",
		metric.WithDescription(
			"Calls to a storage backend by SDK operation and outcome (ok|error). "+
				"Paladin never proxies bytes, so when an object store degrades the "+
				"only thing the RPC layer shows is latency it cannot attribute."),
	); err != nil {
		otel.Handle(err)
	}
	if storageSeconds, err = meterDomain.Float64Histogram(
		"paladin_storage_call_duration_seconds",
		metric.WithDescription(
			"Storage-backend call duration, measured across the whole SDK "+
				"operation — retries included, because the caller waits for those too."),
		metric.WithUnit("s"),
	); err != nil {
		otel.Handle(err)
	}
	if quotaDecisions, err = meterDomain.Int64Counter(
		"paladin_quota_decisions_total",
		metric.WithDescription(
			"Quota enforcement decisions by scope (tenant|bucket) and outcome "+
				"(allowed|rejected). A tenant at its cap and a tenant with a cap set "+
				"to the wrong number are the same ResourceExhausted to the RPC layer."),
	); err != nil {
		otel.Handle(err)
	}
	if loginAttempts, err = meterDomain.Int64Counter(
		"paladin_login_attempts_total",
		metric.WithDescription(
			"Login attempts by outcome (ok|invalid_credentials|audience_denied|"+
				"invalid_argument|error). The failure RATE is the signal — a "+
				"credential-stuffing run is ordinary Login traffic to otelconnect."),
	); err != nil {
		otel.Handle(err)
	}
	if idempotencyLookups, err = meterDomain.Int64Counter(
		"paladin_idempotency_lookups_total",
		metric.WithDescription(
			"Idempotency-key lookups by outcome (replayed|miss|unreplayable). "+
				"`unreplayable` means the key WAS cached but the response could not "+
				"be reconstructed, so the handler ran a second time — the one outcome "+
				"here that can produce a duplicate side effect."),
	); err != nil {
		otel.Handle(err)
	}
}

// The op label of paladin_presign_total: which kind of URL was signed.
const (
	PresignOpGet  = "get"
	PresignOpPut  = "put"
	PresignOpPost = "post" // a browser form upload, the POST policy transport
	PresignOpPart = "part"
)

// PresignOutcomeOK is the outcome label of a presign that produced a URL.
const PresignOutcomeOK = "ok"

// PresignOutcome maps a presign's error to its outcome label: ok, or the
// Connect code of the failure. The code is the right granularity: it separates
// "denied by policy" from "no such object" from "budget exhausted" — three
// different operational problems — without admitting the unbounded set of
// error strings.
func PresignOutcome(err error) string {
	if err == nil {
		return PresignOutcomeOK
	}
	return connect.CodeOf(err).String()
}

// RecordPresign counts one presign and its latency. op is one of the
// PresignOp* constants; outcome is PresignOutcome of the call's error.
func RecordPresign(ctx context.Context, op, outcome string, seconds float64) {
	if presignIssued == nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("op", op),
		attribute.String("outcome", outcome),
	)
	presignIssued.Add(ctx, 1, attrs)
	if presignSeconds != nil {
		presignSeconds.Record(ctx, seconds, attrs)
	}
}

// RecordCapabilityCharge counts a charge attempt. Amount is recorded only for
// a successful one — a rejected charge has no amount, and folding zeros into
// the histogram would drag the distribution toward a number nobody spent.
func RecordCapabilityCharge(ctx context.Context, tenantID, outcome string, amount float64) {
	if capabilityCharges == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	capabilityCharges.Add(ctx, 1, metric.WithAttributes(attrs...))
	if outcome == "charged" && capabilityAmount != nil {
		capabilityAmount.Record(ctx, amount, metric.WithAttributes(attrs...))
	}
}

// RecordObjectLock counts an object-lock write. mode is GOVERNANCE,
// COMPLIANCE, or "" for a legal-hold toggle that names no mode.
func RecordObjectLock(ctx context.Context, op, mode, outcome string) {
	if objectLockOps == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("op", op),
		attribute.String("outcome", outcome),
	}
	if mode != "" {
		attrs = append(attrs, attribute.String("mode", mode))
	}
	objectLockOps.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordStorageCall counts one storage-backend call and its duration. op is
// the SDK operation name (HeadObject, CreateBucket, …); outcome is ok|error.
//
// backendID is the label an operator actually needs — "which store is slow" —
// and it is empty when the client was built outside the registry (the probe
// path constructs one ad hoc). Omitted rather than defaulted in that case, for
// the same reason tenant_id is: a made-up value in a dashboard is worse than a
// missing one.
func RecordStorageCall(ctx context.Context, backendID, op, outcome string, seconds float64) {
	if storageCalls == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("op", op),
		attribute.String("outcome", outcome),
	}
	if backendID != "" {
		attrs = append(attrs, attribute.String("backend_id", backendID))
	}
	storageCalls.Add(ctx, 1, metric.WithAttributes(attrs...))
	if storageSeconds != nil {
		storageSeconds.Record(ctx, seconds, metric.WithAttributes(attrs...))
	}
}

// RecordQuotaDecision counts one enforcement decision. scope is tenant|bucket;
// outcome is allowed|rejected.
//
// Only decisions are counted. A request with no quota row, or one the
// interceptor does not gate, made no decision — counting those as "allowed"
// would bury the real allow/reject ratio under traffic the quota system never
// looked at.
func RecordQuotaDecision(ctx context.Context, tenantID, scope, outcome string) {
	if quotaDecisions == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("scope", scope),
		attribute.String("outcome", outcome),
	}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	quotaDecisions.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordLoginAttempt counts one login by outcome.
//
// Deliberately carries no subject and no tenant. The useful signal is the
// failure rate, and a per-subject label would be both unbounded and a record
// of who is being targeted — which is exactly the data an attacker would like
// out of a metrics endpoint.
func RecordLoginAttempt(ctx context.Context, outcome string) {
	if loginAttempts == nil {
		return
	}
	loginAttempts.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordIdempotencyLookup counts one key lookup by outcome:
// replayed|miss|unreplayable.
//
// method is the RPC procedure, which is bounded by the service surface — the
// KEY is not, and never becomes a label.
func RecordIdempotencyLookup(ctx context.Context, method, outcome string) {
	if idempotencyLookups == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if method != "" {
		attrs = append(attrs, attribute.String("method", method))
	}
	idempotencyLookups.Add(ctx, 1, metric.WithAttributes(attrs...))
}
