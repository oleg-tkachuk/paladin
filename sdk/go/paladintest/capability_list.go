package paladintest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// msgListElsewhere is the server's refusal of another tenant's listing.
const msgListElsewhere = "listing another tenant's capabilities requires platform.admin or platform.capability-issuer"

// Prefixes of the fake's List page tokens, by the order each was handed out
// for: a token of the other order is refused, as the server refuses one. The
// filters it was handed out for follow (listFilters), so a token handed back
// with others is refused too.
const (
	ascendingCursor  = "paladintest_asc_"
	descendingCursor = "paladintest_desc_"
)

// List pages through a tenant's capabilities as the server does: of a
// principal kind and a subject when the request names them, of the
// dispositions it names — the active ones and those the deprecated include
// flags add when it names none — in id order, which is issue order, each
// with its disposition. Revoked is a capability revoked itself or through an
// ancestor, over expired, over active.
func (s capabilityService) List(ctx context.Context, req *adminv1.CapabilityServiceListRequest) (*adminv1.CapabilityServiceListResponse, error) {
	if s.strictAuth {
		caller, err := s.principalTenant(requestHeader(ctx))
		if err != nil {
			return nil, err
		}
		if caller != req.GetTenantId() {
			return nil, withReason(connect.NewError(connect.CodePermissionDenied, msgListElsewhere))
		}
	}
	descending := req.GetSortOrder() == commonv1.SortOrder_SORT_ORDER_DESC
	after, err := listCursor(req.GetPageToken(), descending, listFilters(req))
	if err != nil {
		return nil, err
	}
	wanted := listDispositions(req)

	s.mu.Lock()
	now := time.Now()
	var listed []*adminv1.Capability
	for id, rec := range s.capabilities {
		subject := rec.msg.GetSubject()
		switch {
		case subject.GetTenantId() != req.GetTenantId(),
			req.GetPrincipalKind() != adminv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED && subject.GetKind() != req.GetPrincipalKind(),
			req.GetSubject() != "" && subject.GetSubject() != req.GetSubject(),
			req.GetSession() != "" && rec.msg.GetSession() != req.GetSession(),
			// A UUID's canonical string orders as its bytes.
			after != "" && (strings.Compare(id, after) < 0) != descending,
			id == after:
			continue
		}
		d := s.dispositionOf(rec, now)
		if !slices.Contains(wanted, d) {
			continue
		}
		c := proto.CloneOf(rec.msg)
		c.Disposition = d
		listed = append(listed, c)
	}
	s.mu.Unlock()

	slices.SortFunc(listed, func(a, b *adminv1.Capability) int {
		n := strings.Compare(a.GetId(), b.GetId())
		if descending {
			return -n
		}
		return n
	})
	out := &adminv1.CapabilityServiceListResponse{Capabilities: []*adminv1.Capability{}}
	limit := limes.PageLimit(int(req.GetPageSize()))
	if len(listed) > limit {
		listed = listed[:limit]
		prefix := ascendingCursor
		if descending {
			prefix = descendingCursor
		}
		out.NextPageToken = prefix + listFilters(req) + cursorSeparator + listed[len(listed)-1].GetId()
	}
	out.Capabilities = append(out.Capabilities, listed...)
	return out, nil
}

// listDispositions is what req lists: its dispositions, or, when it names
// none, the active capabilities and those its deprecated include flags add.
func listDispositions(req *adminv1.CapabilityServiceListRequest) []adminv1.CapabilityDisposition {
	if named := req.GetDispositions(); len(named) > 0 {
		return named
	}
	out := []adminv1.CapabilityDisposition{adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE}
	if req.GetIncludeExpired() { //nolint:staticcheck // SA1019: the deprecated flags still widen a request that names no dispositions
		out = append(out, adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED)
	}
	if req.GetIncludeRevoked() { //nolint:staticcheck // SA1019: as above
		out = append(out, adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED)
	}
	return out
}

// cursorSeparator ends a page token's filters, before the id it resumes after.
const cursorSeparator = "_"

// listFilters names what req lists, as the server's cursor holds it: the
// tenant (limes binds a cursor to it), the principal kind, the subject, the
// session and the set of dispositions, in any order.
func listFilters(req *adminv1.CapabilityServiceListRequest) string {
	ds := slices.Clone(listDispositions(req))
	slices.Sort(ds)
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%s\x00%s\x00%v", req.GetTenantId(), req.GetPrincipalKind(), req.GetSubject(), req.GetSession(), ds))
	return hex.EncodeToString(sum[:])
}

// listCursor is the id a page token resumes after, empty for none. A token
// the fake did not hand out for this order and these filters is
// InvalidArgument.
func listCursor(token string, descending bool, filters string) (string, error) {
	if token == "" {
		return "", nil
	}
	prefix := ascendingCursor
	if descending {
		prefix = descendingCursor
	}
	id, ok := strings.CutPrefix(token, prefix+filters+cursorSeparator)
	if _, err := uuid.Parse(id); !ok || err != nil {
		return "", withReason(connect.Errorf(connect.CodeInvalidArgument, "invalid request: record list cursor %q", token))
	}
	return id, nil
}

// dispositionOf is rec's disposition at now. The caller holds mu.
func (s *Server) dispositionOf(rec *capabilityRecord, now time.Time) adminv1.CapabilityDisposition {
	for r := rec; r != nil; r = s.capabilities[r.msg.GetParentId()] {
		if s.credentials[r.credentials[0]].revoked {
			return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED
		}
	}
	if !rec.msg.GetExpiresAt().AsTime().After(now) {
		return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED
	}
	return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE
}
