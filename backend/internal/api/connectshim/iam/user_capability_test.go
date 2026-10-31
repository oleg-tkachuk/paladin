package iam

import (
	"context"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// recordingCapabilities answers ListUserCapabilities with page, keeping what
// it was asked.
type recordingCapabilities struct {
	failingUser
	ref   userh.UserRef
	asked userh.UserCapabilitiesPage
	page  limes.Page[limes.RecordListing]
}

func (r *recordingCapabilities) ListUserCapabilities(_ context.Context, ref userh.UserRef, page userh.UserCapabilitiesPage) (limes.Page[limes.RecordListing], error) {
	r.ref, r.asked = ref, page
	return r.page, nil
}

func TestListUserCapabilities_ForwardsThePageAndAnswersIt(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	h := &recordingCapabilities{page: limes.Page[limes.RecordListing]{
		Items: []limes.RecordListing{{Disposition: limes.RecordDispositionActive}},
		Next:  "next",
	}}
	srv := &UserServer{H: h}
	got, err := srv.ListUserCapabilities(context.Background(), &pb.ListUserCapabilitiesRequest{
		Name:      "tenants/" + tenant.String() + "/users/" + user.String(),
		States:    []pb.UserCapability_State{pb.UserCapability_STATE_EXPIRED, pb.UserCapability_STATE_REVOKED},
		PageSize:  5,
		PageToken: "cursor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.ref.UserID != user {
		t.Errorf("listed for %s, want %s", h.ref.UserID, user)
	}
	wantDispositions := []limes.RecordDisposition{limes.RecordDispositionExpired, limes.RecordDispositionRevoked}
	if !slices.Equal(h.asked.Dispositions, wantDispositions) || h.asked.Size != 5 || h.asked.Token != "cursor" {
		t.Errorf("asked %+v, want dispositions %v, size 5, token cursor", h.asked, wantDispositions)
	}
	if len(got.GetCapabilities()) != 1 || got.GetNextPageToken() != "next" {
		t.Errorf("answered %d capabilities, next %q; want 1, next", len(got.GetCapabilities()), got.GetNextPageToken())
	}
}

func TestUserCapabilityToProto(t *testing.T) {
	issued := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	revoked := issued.Add(time.Minute)
	parent, id := uuid.New(), uuid.New()
	listing := limes.RecordListing{
		Record: limes.Record{
			Capability: limes.Capability{
				ID: id, ParentID: parent, ClientID: "agent", Audience: []string{"paladin-data"},
				IssuedAt: issued, ExpiresAt: issued.Add(time.Hour),
				Caveats: limes.Caveats{Ops: []limes.Op{"read", "list"}},
			},
			Session: "grant-1",
		},
		Disposition: limes.RecordDispositionRevoked,
		// Stopped through its parent: the parent's entry is the one shown.
		StoppedBy: &limes.CapabilityRevocation{CapabilityID: parent, Revocation: limes.Revocation{RevokedAt: revoked, Note: "grant revoked"}},
	}
	got := userCapabilityToProto(&listing)
	if got.GetId() != id.String() || got.GetParentId() != parent.String() || got.GetClientId() != "agent" || got.GetGrantId() != "grant-1" {
		t.Errorf("identity = %s parent %s client %s grant %s", got.GetId(), got.GetParentId(), got.GetClientId(), got.GetGrantId())
	}
	if !slices.Equal(got.GetAudience(), []string{"paladin-data"}) || !slices.Equal(got.GetOperations(), []string{"read", "list"}) {
		t.Errorf("audience %v operations %v", got.GetAudience(), got.GetOperations())
	}
	if !got.GetIssuedAt().AsTime().Equal(issued) || !got.GetExpiresAt().AsTime().Equal(issued.Add(time.Hour)) {
		t.Errorf("issued %v expires %v", got.GetIssuedAt().AsTime(), got.GetExpiresAt().AsTime())
	}
	if got.GetState() != pb.UserCapability_STATE_REVOKED || !got.GetRevokedAt().AsTime().Equal(revoked) || got.GetRevocationReason() != "grant revoked" {
		t.Errorf("state %v revoked %v reason %q", got.GetState(), got.GetRevokedAt(), got.GetRevocationReason())
	}

	// A capability exchanged directly, still active: no parent, no revocation.
	active := userCapabilityToProto(&limes.RecordListing{Record: limes.Record{Capability: limes.Capability{ID: id}}, Disposition: limes.RecordDispositionActive})
	if active.GetParentId() != "" || active.GetRevokedAt() != nil || active.GetRevocationReason() != "" {
		t.Errorf("an active root capability: parent %q revoked %v reason %q", active.GetParentId(), active.GetRevokedAt(), active.GetRevocationReason())
	}
}

// Every disposition has a wire name, every wire name but UNSPECIFIED is some
// disposition's, and each reads back to the one it came from: one left out
// would reach the console as UNSPECIFIED, or a filter as no disposition.
func TestUserCapabilityState_CoversEveryDisposition(t *testing.T) {
	dispositions := []limes.RecordDisposition{limes.RecordDispositionActive, limes.RecordDispositionExpired, limes.RecordDispositionRevoked}
	seen := map[pb.UserCapability_State]bool{}
	for _, d := range dispositions {
		got := userCapabilityToProto(&limes.RecordListing{Disposition: d}).GetState()
		if got == pb.UserCapability_STATE_UNSPECIFIED {
			t.Errorf("disposition %v reaches the wire as UNSPECIFIED", d)
		}
		if back := userCapabilityStateFromProto[got]; back != d {
			t.Errorf("%v reads back as %v, want %v", got, back, d)
		}
		seen[got] = true
	}
	for v := range pb.UserCapability_State_name {
		w := pb.UserCapability_State(v)
		if w != pb.UserCapability_STATE_UNSPECIFIED && !seen[w] {
			t.Errorf("%v names no disposition", w)
		}
	}
}
