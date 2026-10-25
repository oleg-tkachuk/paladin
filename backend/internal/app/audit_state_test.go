package app

import (
	"sort"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1" // registers the admin contract
	adminconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// noStateReader names each audited admin action that records no state, and
// why.
var noStateReader = map[string]string{
	adminconnect.BackendServiceTestBackendProcedure:                       "probes the backend and changes nothing of it",
	adminconnect.BucketServiceRevokeBucketGrantProcedure:                  "a grant has no Get to read it by; the request names the grant it removes",
	adminconnect.APITokenServiceRevokeProcedure:                           "the token's revocation is its own record, which Get shows",
	adminconnect.CapabilityServiceRevokeProcedure:                         "the answer says what the capability was before and what stopped it",
	adminconnect.CapabilityServiceRevokeBiscuitProcedure:                  "a copy of a Biscuit has no record of its own to read",
	adminconnect.EventSubscriptionServiceRedriveFailedDeliveriesProcedure: "acts on deliveries, not on the subscription",
	adminconnect.EventSubscriptionServiceTestSubscriptionProcedure:        "sends a test event; the subscription is unchanged",
	adminconnect.OAuthClientServiceDeleteOAuthClientProcedure:             "a client has no Get to read it by; the request names the client removed",
	adminconnect.PlatformOperationServiceCancelOperationProcedure:         "an operation's own record is its history",
	adminconnect.PlatformOperationServiceDismissOperationsProcedure:       "an operation's own record is its history",
}

// Every admin action the log records either records the resource it changes
// before and after, or says here why it does not. A new RPC that changes a
// resource is caught where it is added, not when someone reads its entry and
// finds no state.
func TestEveryAuditedAdminActionHasAStateReaderOrSaysWhyNot(t *testing.T) {
	readers := middleware.StateReaders{}
	adminStateServers{
		backend: &admin.BackendServer{}, bucket: &admin.BucketServer{}, tenant: &admin.TenantServer{},
		collection: &admin.CollectionServer{}, quota: &admin.QuotaServer{}, budget: &admin.TenantBudgetServer{},
		subscription: &admin.EventSubscriptionServer{},
	}.fill(readers)

	var missing, needless []string
	audited := map[string]bool{}
	protoregistry.GlobalFiles.RangeFilesByPackage(protoreflect.FullName(adminPackage), func(f protoreflect.FileDescriptor) bool {
		for i := range f.Services().Len() {
			svc := f.Services().Get(i)
			for j := range svc.Methods().Len() {
				m := svc.Methods().Get(j)
				procedure := "/" + string(svc.FullName()) + "/" + string(m.Name())
				level := connect.IdempotencyUnknown
				if opts, ok := m.Options().(*descriptorpb.MethodOptions); ok && opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
					level = connect.IdempotencyNoSideEffects
				}
				if middleware.AuditSkips(procedure, level) {
					continue
				}
				audited[procedure] = true
				_, reads := readers[procedure]
				_, says := noStateReader[procedure]
				switch {
				case !reads && !says:
					missing = append(missing, procedure)
				case reads && says:
					needless = append(needless, procedure)
				}
			}
		}
		return true
	})
	sort.Strings(missing)
	for _, p := range missing {
		t.Errorf("%s records no state and does not say why: give it a reader (adminStateServers.fill) or a reason in noStateReader", p)
	}
	for _, p := range needless {
		t.Errorf("%s has a reader and a reason not to; drop the reason", p)
	}
	for p := range noStateReader {
		if !audited[p] {
			t.Errorf("noStateReader names %s, which the log does not record", p)
		}
	}
	for p := range readers {
		if !audited[p] {
			t.Errorf("a reader for %s, which the log does not record", p)
		}
	}
}
