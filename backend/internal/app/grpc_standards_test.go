package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// Each plane lists its own package's services and none of another's.
func TestServicesInListsAPlanesPackage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		got     []string
		want    string
		foreign string
	}{
		{"data", servicesIn(protoregistry.GlobalFiles, dataPackage), paladindatav1connect.ObjectServiceName, paladiniamv1connect.AuthServiceName},
		{"iam", servicesIn(protoregistry.GlobalFiles, iamPackage), paladiniamv1connect.AuthServiceName, paladinadminv1connect.TenantServiceName},
		{"admin", servicesIn(protoregistry.GlobalFiles, adminPackage), paladinadminv1connect.TenantServiceName, paladindatav1connect.ObjectServiceName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Contains(tc.got, tc.want) || slices.Contains(tc.got, tc.foreign) {
				t.Fatalf("%s services = %v", tc.name, tc.got)
			}
		})
	}
}

// grpcurl and Kubernetes' gRPC probe reach a plane with the standard
// protocols: health answers, and reflection names the plane's services.
func TestAPlaneServesGRPCHealthAndReflection(t *testing.T) {
	services := servicesIn(protoregistry.GlobalFiles, iamPackage)
	mux := http.NewServeMux()
	mountGRPCStandards(mux, (&health.Handler{}).GRPCChecker(services...), services)
	// Reflection is a bidirectional stream, which needs HTTP/2.
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	ctx := context.Background()

	resp, err := grpchealth.NewClient(srv.Client(), srv.URL).Check(ctx, &grpchealth.CheckRequest{Service: paladiniamv1connect.AuthServiceName})
	if err != nil || resp.Status != grpchealth.StatusServing {
		t.Fatalf("health = %v, %v; want serving", resp, err)
	}

	stream := grpcreflect.NewClient(srv.Client(), srv.URL).NewStream(ctx)
	t.Cleanup(func() { _, _ = stream.Close() })
	names, err := stream.ListServices()
	if err != nil {
		t.Fatal(err)
	}
	listed := make([]string, len(names))
	for i, n := range names {
		listed[i] = string(n)
	}
	slices.Sort(listed)
	if !slices.Equal(listed, services) {
		t.Fatalf("reflection lists %v, want %v", listed, services)
	}
}
