package paladintest_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes/meter"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// A request made under a lease of requests is spent from the lease, and one
// past what it holds is refused, as the server's store spends it: the fake
// counts usage through limes's own in-memory store, not a model of its own.
// The Python fake is held to the same (test_testing_caveats.py).
func TestARequestUnderALeaseIsSpentFromIt(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	p, c := strictCapability(t, srv, nil)
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	const leased = 1
	if _, err := admin.Lease(context.Background(), &adminv1.CapabilityServiceLeaseRequest{Id: c.GetId(), Requests: leased}); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := getObject(p, obj); err != nil {
		t.Fatalf("a request under the lease: %v", err)
	}
	got, err := admin.GetLease(context.Background(), &adminv1.CapabilityServiceGetLeaseRequest{Id: c.GetId()})
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if l := got.GetLease(); l.GetRequests() != leased || l.GetRequestsSpent() != leased {
		t.Errorf("lease holds %d requests, %d spent; want %d, %d", l.GetRequests(), l.GetRequestsSpent(), leased, leased)
	}
	err = getObject(p, obj)
	if connect.CodeOf(err) != connect.CodeResourceExhausted || paladin.LimesCode(err) != string(meter.CodeLeaseRequestsExhausted) {
		t.Errorf("a request past the lease: %v (%s, %q), want ResourceExhausted, %s",
			err, connect.CodeOf(err), paladin.LimesCode(err), meter.CodeLeaseRequestsExhausted)
	}
}
