package app

import (
	"net/http"
	"sort"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"connectrpc.com/grpcreflect/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	admindesc "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	datadesc "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamdesc "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// The proto package each plane serves, read from a generated file of it so
// the name cannot drift from the contract.
var (
	dataPackage  = datadesc.File_paladin_data_v1_object_service_proto.Package()
	iamPackage   = iamdesc.File_paladin_iam_v1_auth_service_proto.Package()
	adminPackage = admindesc.File_paladin_admin_v1_tenant_service_proto.Package()
)

// servicesIn lists the services the contract declares in pkg, sorted.
func servicesIn(files *protoregistry.Files, pkg protoreflect.FullName) []string {
	var out []string
	files.RangeFilesByPackage(pkg, func(f protoreflect.FileDescriptor) bool {
		for i := range f.Services().Len() {
			out = append(out, string(f.Services().Get(i).FullName()))
		}
		return true
	})
	sort.Strings(out)
	return out
}

// mountGRPCStandards serves, beside a plane's own services, the two gRPC
// standards tools expect: the health protocol (grpc.health.v1), answering as
// /readyz does, and server reflection for the plane's services, so grpcurl
// and similar clients need no copy of the contract. Neither passes through
// the plane's interceptors — like /readyz they are unauthenticated — and
// reflection discloses nothing the public contract does not.
//
// They get a server of their own for that reason, and reflection names the
// plane's services rather than its own: by default it would describe the
// services registered beside it, the two standards themselves.
func mountGRPCStandards(mux *http.ServeMux, checker grpchealth.Checker, services []string) {
	server := connect.NewServer()
	grpchealth.Register(server, checker)
	grpcreflect.Register(server, grpcreflect.WithNamer(grpcreflect.NamerFunc(func() []string { return services })))
	connecthttp.Mount(mux, server, rpcMountOptions()...)
}
