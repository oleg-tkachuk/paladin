package app

import (
	"sort"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	connectdata "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1" // registers the admin contract
	adminconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1" // registers the data contract
	dataconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1" // registers the IAM contract
	iamconnect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// noStateReader names each audited action, on every plane, that records no
// state, and why.
var noStateReader = map[string]string{
	adminconnect.BackendServiceTestBackendProcedure:                       "probes the backend and changes nothing of it",
	adminconnect.BucketServiceRevokeBucketGrantProcedure:                  "a grant has no Get to read it by; the request names the grant it removes",
	adminconnect.APITokenServiceRevokeProcedure:                           "the token's revocation is its own record, which Get shows",
	adminconnect.CapabilityServiceRevokeProcedure:                         "the answer says what the capability was before and what stopped it",
	adminconnect.CapabilityServiceRevokeBiscuitProcedure:                  "a copy of a Biscuit has no record of its own to read",
	adminconnect.CapabilityServiceRevokeRunProcedure:                      "a run has no record of its own; the answer counts what it revoked",
	adminconnect.CapabilityServiceLeaseProcedure:                          "a lease has no Get on the record it changes; the answer is the lease as it stands",
	adminconnect.CapabilityServiceSettleLeaseProcedure:                    "the answer is the lease as it ended",
	adminconnect.EventSubscriptionServiceRedriveFailedDeliveriesProcedure: "acts on deliveries, not on the subscription",
	adminconnect.EventSubscriptionServiceTestSubscriptionProcedure:        "sends a test event; the subscription is unchanged",
	adminconnect.OAuthClientServiceDeleteOAuthClientProcedure:             "a client has no Get to read it by; the request names the client removed",
	adminconnect.PlatformOperationServiceCancelOperationProcedure:         "an operation's own record is its history",
	adminconnect.PlatformOperationServiceDismissOperationsProcedure:       "an operation's own record is its history",

	// The data plane.
	dataconnect.BatchServiceBatchCopyObjectsProcedure:               "starts an operation over many objects; the operation records what each came to",
	dataconnect.BatchServiceBatchDeleteObjectsProcedure:             "starts an operation over many objects; the operation records what each came to",
	dataconnect.BatchServiceBatchRestoreObjectsProcedure:            "starts an operation over many objects; the operation records what each came to",
	dataconnect.BatchServiceBatchUpdateTagsProcedure:                "starts an operation over many objects; the operation records what each came to",
	dataconnect.MultipartUploadServiceAbortMultipartUploadProcedure: "an upload in progress has no Get to read it by; no object is changed",
	dataconnect.OperationServiceCancelOperationProcedure:            "an operation's own record is its history",
	dataconnect.OperationServiceDismissOperationsProcedure:          "an operation's own record is its history",
	dataconnect.PresignServiceRegenerateUploadUrlProcedure:          "issues a URL for an upload already begun; no object is changed",
	dataconnect.StorageBootstrapServiceEnsureTenantStorageProcedure: "provisions the tenant's bucket on the store, which has no Get of its own here",

	// The IAM plane.
	iamconnect.AuthServiceChangePasswordProcedure:   "changes a secret, which no Get shows",
	iamconnect.AuthServiceRevokeProcedure:           "ends a session, which has no Get to read it by",
	iamconnect.AuthServiceSwitchTenantProcedure:     "issues a session for another tenant; nothing is changed",
	iamconnect.UserServiceResetPasswordProcedure:    "changes a secret, which no Get shows",
	iamconnect.UserServiceRevokeOAuthGrantProcedure: "a grant has no Get to read it by; the request names the grant revoked",
}

// Every action the log records, on every plane, either records the resource
// it changes before and after, or says here why it does not. A new RPC that
// changes a resource is caught where it is added, not when someone reads its
// entry and finds no state.
func TestEveryAuditedActionHasAStateReaderOrSaysWhyNot(t *testing.T) {
	adminReaders := middleware.StateReaders{}
	adminStateServers{
		backend: &admin.BackendServer{}, bucket: &admin.BucketServer{}, tenant: &admin.TenantServer{},
		collection: &admin.CollectionServer{}, quota: &admin.QuotaServer{}, budget: &admin.TenantBudgetServer{},
		subscription: &admin.EventSubscriptionServer{},
	}.fill(adminReaders)
	data := middleware.StateReaders{}
	dataStateServers{object: &connectdata.ObjectServer{}, tags: &connectdata.ObjectTagServer{}}.fill(data)
	iam := middleware.StateReaders{}
	iamStateServers{user: &connectiam.UserServer{}, settings: &connectiam.UserSettingsServer{}}.fill(iam)

	for _, plane := range []struct {
		pkg     protoreflect.FullName
		readers middleware.StateReaders
	}{{adminPackage, adminReaders}, {dataPackage, data}, {iamPackage, iam}} {
		t.Run(string(plane.pkg), func(t *testing.T) { checkStateReaders(t, plane.pkg, plane.readers) })
	}
}

// checkStateReaders holds each audited procedure of pkg to a reader in
// readers or a reason in noStateReader.
func checkStateReaders(t *testing.T, pkg protoreflect.FullName, readers middleware.StateReaders) {
	t.Helper()
	var missing, needless []string
	audited := map[string]bool{}
	protoregistry.GlobalFiles.RangeFilesByPackage(pkg, func(f protoreflect.FileDescriptor) bool {
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
		t.Errorf("%s records no state and does not say why: give it a reader or a reason in noStateReader", p)
	}
	for _, p := range needless {
		t.Errorf("%s has a reader and a reason not to; drop the reason", p)
	}
	for p := range readers {
		if !audited[p] {
			t.Errorf("a reader for %s, which the log does not record", p)
		}
	}
}

// Every reason names an action the log records.
func TestNoStateReaderNamesAuditedActions(t *testing.T) {
	audited := map[string]bool{}
	for _, pkg := range []protoreflect.FullName{adminPackage, dataPackage, iamPackage} {
		protoregistry.GlobalFiles.RangeFilesByPackage(pkg, func(f protoreflect.FileDescriptor) bool {
			for i := range f.Services().Len() {
				svc := f.Services().Get(i)
				for j := range svc.Methods().Len() {
					audited["/"+string(svc.FullName())+"/"+string(svc.Methods().Get(j).Name())] = true
				}
			}
			return true
		})
	}
	for p := range noStateReader {
		if !audited[p] {
			t.Errorf("noStateReader names %s, which no plane serves", p)
		}
	}
}

func TestObjectOfVersion(t *testing.T) {
	const object = "tenants/t/collections/c/objects/o"
	for in, want := range map[string]string{
		object + versionSegment + "v": object,
		object:                        object,
	} {
		if got := objectOfVersion(in); got != want {
			t.Errorf("objectOfVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
