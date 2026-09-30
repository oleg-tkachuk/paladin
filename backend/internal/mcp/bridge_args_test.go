package mcp

import (
	"strings"
	"testing"

	adminv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/common/v1"
)

// TestStringScopesToProto covers the "type:value" mini-language agents type
// into capability and token tools. Every rejection here is a request that would
// otherwise reach the plane as a silently wrong scope — "tenant" with no colon
// parsed as a bare value, say — so the error cases carry the weight.
func TestStringScopesToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []string
		want    []*commonv1.Scope
		wantErr string
	}{
		{
			name: "nil input yields empty, not nil-deref",
			in:   nil,
			want: []*commonv1.Scope{},
		},
		{
			name: "empty strings are skipped, not errors",
			in:   []string{"", "tenant:t1", ""},
			want: []*commonv1.Scope{{Type: commonv1.ScopeType_SCOPE_TYPE_TENANT, Value: "t1"}},
		},
		{
			name: "wildcard keeps an unset type",
			in:   []string{"*"},
			want: []*commonv1.Scope{{Value: "*"}},
		},
		{
			name: "every known scope type",
			in:   []string{"tenant:t1", "backend:b1", "bucket:bk1", "collection:c1"},
			want: []*commonv1.Scope{
				{Type: commonv1.ScopeType_SCOPE_TYPE_TENANT, Value: "t1"},
				{Type: commonv1.ScopeType_SCOPE_TYPE_BACKEND, Value: "b1"},
				{Type: commonv1.ScopeType_SCOPE_TYPE_BUCKET, Value: "bk1"},
				{Type: commonv1.ScopeType_SCOPE_TYPE_OBJECT_KEY, Value: "c1"},
			},
		},
		{
			name: "value may contain colons — only the first splits",
			in:   []string{"bucket:s3://a:b/c"},
			want: []*commonv1.Scope{{Type: commonv1.ScopeType_SCOPE_TYPE_BUCKET, Value: "s3://a:b/c"}},
		},
		{
			name: "empty value is preserved rather than rejected",
			in:   []string{"tenant:"},
			want: []*commonv1.Scope{{Type: commonv1.ScopeType_SCOPE_TYPE_TENANT}},
		},
		{
			name:    "missing colon",
			in:      []string{"tenant"},
			wantErr: `must be type:value`,
		},
		{
			name:    "unknown type",
			in:      []string{"realm:r1"},
			wantErr: `unknown scope type "realm"`,
		},
		{
			name:    "wildcard is exact — a prefix is not a wildcard",
			in:      []string{"*:t1"},
			wantErr: `unknown scope type "*"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stringScopesToProto(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("stringScopesToProto(%v) = %v, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("stringScopesToProto(%v): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d scopes, want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].GetType() != tc.want[i].GetType() || got[i].GetValue() != tc.want[i].GetValue() {
					t.Errorf("scope[%d] = {%v %q}, want {%v %q}",
						i, got[i].GetType(), got[i].GetValue(), tc.want[i].GetType(), tc.want[i].GetValue())
				}
			}
		})
	}
}

// TestBuildEventSubscription pins the sink union. The rule "exactly one sink"
// is the whole point: zero sinks would create a subscription that delivers
// nowhere, and two would leave the winner up to field order in the admin
// handler. Half-configured sinks are rejected for the same reason — a Kafka
// sink with brokers but no topic is not a usable subscription.
func TestBuildEventSubscription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      createSubscriptionArgs
		check   func(*testing.T, *adminv1.EventSubscription)
		wantErr string
	}{
		{
			name: "http sink carries its retry cap and secret ref",
			in: createSubscriptionArgs{
				TenantID: "t1", Filter: "type == 'object.created'", Disabled: true,
				HTTPURL: "https://hooks.example/x", HTTPSecretRef: "sec1", HTTPMaxAttempts: 7,
			},
			check: func(t *testing.T, s *adminv1.EventSubscription) {
				t.Helper()
				h := s.GetSink().GetHttp()
				if h == nil {
					t.Fatalf("sink is %T, want http", s.GetSink().GetTarget())
				}
				if h.GetUrl() != "https://hooks.example/x" || h.GetSigningSecretRef() != "sec1" || h.GetMaxAttempts() != 7 {
					t.Errorf("http sink = %+v", h)
				}
				if s.GetTenantId() != "t1" || s.GetFilter() != "type == 'object.created'" || !s.GetDisabled() {
					t.Errorf("envelope fields not carried through: %+v", s)
				}
			},
		},
		{
			name: "kafka sink",
			in:   createSubscriptionArgs{TenantID: "t1", KafkaBrokers: "b:9092", KafkaTopic: "events"},
			check: func(t *testing.T, s *adminv1.EventSubscription) {
				t.Helper()
				k := s.GetSink().GetKafka()
				if k == nil || k.GetBrokers() != "b:9092" || k.GetTopic() != "events" {
					t.Errorf("kafka sink = %+v", k)
				}
			},
		},
		{
			name: "sqs sink",
			in:   createSubscriptionArgs{TenantID: "t1", SQSQueueURL: "https://sqs/q", SQSRegion: "us-east-1"},
			check: func(t *testing.T, s *adminv1.EventSubscription) {
				t.Helper()
				q := s.GetSink().GetSqs()
				if q == nil || q.GetQueueUrl() != "https://sqs/q" || q.GetRegion() != "us-east-1" {
					t.Errorf("sqs sink = %+v", q)
				}
			},
		},
		{
			name:    "no sink",
			in:      createSubscriptionArgs{TenantID: "t1"},
			wantErr: "got 0 configured",
		},
		{
			name:    "two sinks",
			in:      createSubscriptionArgs{TenantID: "t1", HTTPURL: "https://h", KafkaTopic: "events"},
			wantErr: "got 2 configured",
		},
		{
			name:    "all three sinks",
			in:      createSubscriptionArgs{TenantID: "t1", HTTPURL: "https://h", KafkaTopic: "e", SQSRegion: "us-east-1"},
			wantErr: "got 3 configured",
		},
		{
			name:    "kafka missing topic",
			in:      createSubscriptionArgs{TenantID: "t1", KafkaBrokers: "b:9092"},
			wantErr: "requires both kafka_brokers and kafka_topic",
		},
		{
			name:    "kafka missing brokers",
			in:      createSubscriptionArgs{TenantID: "t1", KafkaTopic: "events"},
			wantErr: "requires both kafka_brokers and kafka_topic",
		},
		{
			name:    "sqs missing region",
			in:      createSubscriptionArgs{TenantID: "t1", SQSQueueURL: "https://sqs/q"},
			wantErr: "requires both sqs_queue_url and sqs_region",
		},
		{
			name:    "sqs missing queue url",
			in:      createSubscriptionArgs{TenantID: "t1", SQSRegion: "us-east-1"},
			wantErr: "requires both sqs_queue_url and sqs_region",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildEventSubscription(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("buildEventSubscription succeeded (%+v), want error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildEventSubscription: %v", err)
			}
			tc.check(t, got)
		})
	}
}
