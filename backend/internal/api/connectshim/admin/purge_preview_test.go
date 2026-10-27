package admin

import (
	"testing"
	"uuid"

	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The preview names each token and subscription by the resource name its own
// service takes, so the console can link to it, and lists a blocker only for
// a kind the tenant holds, in the order they are removed in.
func TestPurgePreviewToProto(t *testing.T) {
	tenant, token, sub := uuid.New(), uuid.New(), uuid.New()
	got := purgePreviewToProto(tenant, &tenanth.PurgePreview{
		APITokens:         []tenanth.PurgedAPIToken{{ID: token, Name: "ci"}},
		APITokenCount:     60,
		Subscriptions:     []tenanth.PurgedSubscription{{ID: sub, SinkKind: "webhook"}},
		SubscriptionCount: 1,
		CapabilityCount:   9,
		Blockers:          tenanth.PurgeBlockers{Collections: 2, Users: 3},
	})
	want := &pb.PreviewPurgeTenantResponse{
		ApiTokens:              []*pb.PurgedResource{{Name: "tenants/" + tenant.String() + "/apiTokens/" + token.String(), DisplayName: "ci"}},
		ApiTokenCount:          60,
		EventSubscriptions:     []*pb.PurgedResource{{Name: "tenants/" + tenant.String() + "/eventSubscriptions/" + sub.String(), DisplayName: "webhook"}},
		EventSubscriptionCount: 1,
		CapabilityCount:        9,
		Blockers: []*pb.PurgeBlocker{
			{Kind: pb.PurgeBlockerKind_PURGE_BLOCKER_KIND_COLLECTIONS, Count: 2},
			{Kind: pb.PurgeBlockerKind_PURGE_BLOCKER_KIND_USERS, Count: 3},
		},
	}
	if !proto.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if empty := purgePreviewToProto(tenant, &tenanth.PurgePreview{}); len(empty.GetBlockers()) != 0 {
		t.Errorf("a tenant holding nothing has blockers %v, want none", empty.GetBlockers())
	}
}
