package middleware

import (
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// The audit row stores the request. Before redaction it stored every
// ExchangeAudience refresh token and every CreateUser initial password in
// plaintext, readable through ListAuditLog.

const secretValue = "s3cret-value-that-must-not-be-stored"

func TestAuditPayloadRedactsMarkedFields(t *testing.T) {
	for name, msg := range map[string]interface{ ProtoReflect() protoreflect.Message }{
		"refresh token":    &iamv1.ExchangeAudienceRequest{RefreshToken: secretValue, TargetAudience: "paladin-data"},
		"initial password": &iamv1.CreateUserRequest{Subject: "alice", InitialPassword: secretValue},
		"login password":   &iamv1.LoginRequest{Subject: "alice", Password: secretValue},
		"nested sink secret": &adminv1.CreateSubscriptionRequest{
			Parent: "tenants/acme",
			Subscription: &adminv1.EventSubscription{
				Sink: &adminv1.EventSink{Target: &adminv1.EventSink_Kafka{Kafka: &adminv1.KafkaSink{
					Brokers: "b:9092", Topic: "t", SaslPassword: secretValue,
				}}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := string(marshalAuditPayload(msg))
			if strings.Contains(got, secretValue) {
				t.Fatalf("audit payload carries the secret: %s", got)
			}
			if got == "" || got == "{}" {
				t.Fatalf("audit payload lost the non-secret fields: %q", got)
			}
		})
	}
}

func TestAuditRedactionLeavesTheRequestIntact(t *testing.T) {
	req := &iamv1.ExchangeAudienceRequest{RefreshToken: secretValue}
	_ = marshalAuditPayload(req)
	if req.GetRefreshToken() != secretValue {
		t.Fatal("redaction cleared the field on the request the handler still has to read")
	}
}

// credentialName matches field names that hold a credential in this API.
var credentialName = regexp.MustCompile(`(^|_)(password|secret|token|private_key|client_key)$|^upstream_code$`)

// pageCursor ends the names of pagination cursors: opaque, issued by the
// server, and no credential.
const pageCursor = "page_token"

// Every string or bytes field the API names like a credential is marked
// debug_redact. A new password or token field that is not would reach the
// audit log in plaintext. Message-typed fields (api_token: the token's
// metadata) are walked into instead; their own fields are checked.
func TestEveryCredentialFieldIsRedacted(t *testing.T) {
	var unmarked []string
	seen := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		var walk func(protoreflect.MessageDescriptors)
		walk = func(msgs protoreflect.MessageDescriptors) {
			for i := range msgs.Len() {
				md := msgs.Get(i)
				fields := md.Fields()
				for j := range fields.Len() {
					f := fields.Get(j)
					if f.Kind() != protoreflect.StringKind && f.Kind() != protoreflect.BytesKind {
						continue
					}
					if !credentialName.MatchString(string(f.Name())) || strings.HasSuffix(string(f.Name()), pageCursor) {
						continue
					}
					seen++
					if opts, ok := f.Options().(*descriptorpb.FieldOptions); !ok || !opts.GetDebugRedact() {
						unmarked = append(unmarked, string(f.FullName()))
					}
				}
				walk(md.Messages())
			}
		}
		walk(fd.Messages())
		return true
	})
	if seen == 0 {
		t.Fatal("matched no fields; the check is asserting nothing")
	}
	if len(unmarked) > 0 {
		t.Errorf("credential fields without debug_redact (mark them debug_redact in the proto):\n  %s",
			strings.Join(unmarked, "\n  "))
	}
}
