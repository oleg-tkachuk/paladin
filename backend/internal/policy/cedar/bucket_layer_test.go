package cedar

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A bucket's policy is stored by SetBucketPolicy and, until it became a layer,
// read by nothing: an operator could forbid deletes in a bucket and every
// delete still went through. It is now compiled into every collection-scoped
// request for a collection bound to that bucket.

func memberOf(tenantID uuid.UUID) *Principal {
	return &Principal{Subject: "member", TenantID: tenantID, TenantSlug: "acme", Kind: "user"}
}

func TestBucketLayerForbidAppliesToCollectionRequests(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Tenant: `permit(principal, action, resource);`,
		Bucket: `forbid(principal, action == Action::"DeleteObject", resource);`,
	}}, 0)

	if got := decideOn(t, e, memberOf(tid), ActionDeleteObject, tid, "docs"); got != DecisionDeny {
		t.Errorf("DeleteObject under a bucket forbid = %v, want Deny", got)
	}
	if got := decideOn(t, e, memberOf(tid), ActionGetObject, tid, "docs"); got != DecisionAllow {
		t.Errorf("GetObject, which the bucket does not forbid = %v, want Allow", got)
	}
}

func TestBucketLayerPermitGrantsWithinTheBucket(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Bucket: `permit(principal, action == Action::"GetObject", resource);`,
	}}, 0)
	if got := decideOn(t, e, memberOf(tid), ActionGetObject, tid, "docs"); got != DecisionAllow {
		t.Errorf("GetObject under a bucket permit = %v, want Allow", got)
	}
}

// A bucket layer that does not parse freezes the collections in that bucket,
// and is reported as the bucket layer, not blamed on another.
func TestBrokenBucketLayerFreezesItsCollections(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Tenant: `permit(principal, action, resource);`,
		Bucket: `permit(principal);`,
	}}, 0)
	if got := decideOn(t, e, memberOf(tid), ActionGetObject, tid, "docs"); got != DecisionDeny {
		t.Errorf("GetObject under an unparseable bucket layer = %v, want Deny", got)
	}

	_, degraded := (&Engine{}).degradeUnparseableLayers(Layers{Bucket: `permit(principal);`}, tid, "docs")
	if len(degraded) != 1 || degraded[0] != layerBucket {
		t.Errorf("degraded = %v, want only %q", degraded, layerBucket)
	}
}

// Layers join in the order they narrow: tenant, bucket, collection.
func TestLayersJoinTenantBucketCollection(t *testing.T) {
	text, _ := (&Engine{}).degradeUnparseableLayers(Layers{
		Tenant:     `permit(principal, action == Action::"ReadTenant", resource);`,
		Bucket:     `permit(principal, action == Action::"GetObject", resource);`,
		Collection: `permit(principal, action == Action::"PutObject", resource);`,
	}, uuid.New(), "docs")
	tenantAt := strings.Index(text, "ReadTenant")
	bucketAt := strings.Index(text, bucketLayerMarker)
	collectionAt := strings.Index(text, collectionLayerMarker)
	if tenantAt < 0 || bucketAt < tenantAt || collectionAt < bucketAt {
		t.Errorf("layer order wrong in:\n%s", text)
	}
}

// A bucket policy change reaches every tenant with a collection in the
// bucket, which the engine cannot enumerate, so it drops the whole cache —
// without counting it as a dropped LISTEN connection.
func TestAllScopesEventFlushesEveryTenant(t *testing.T) {
	events := make(chan ChangeEvent)
	e := NewEngine(watchStore{ch: events}, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	a, b := uuid.New(), uuid.New()
	e.compiled.Store(cacheKey{tenant: a, collection: "x"}, &compiledPolicy{})
	e.compiled.Store(cacheKey{tenant: b, collection: "y"}, &compiledPolicy{})

	events <- ChangeEvent{AllScopes: true}
	waitTenantEntries(t, e, a, 0)
	waitTenantEntries(t, e, b, 0)
	if n := e.m.watchResyncs.Load(); n != 0 {
		t.Errorf("watchResyncs = %d, want 0: a policy change is not a reconnect", n)
	}
}

func TestNotifyPayloadForAllScopes(t *testing.T) {
	if ev := parseNotifyPayload(NotifyAllScopes); !ev.AllScopes || ev.TenantID != uuid.Nil {
		t.Errorf("parse(%q) = %+v, want AllScopes", NotifyAllScopes, ev)
	}
	tid := uuid.New()
	if ev := parseNotifyPayload(tid.String()); ev.AllScopes || ev.TenantID != tid {
		t.Errorf("a tenant payload parsed as %+v", ev)
	}
}
