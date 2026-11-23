package capabilityh

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

func TestTTLOf(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		want    time.Duration
		ok      bool
	}{
		{0, 0, true},
		{300, 5 * time.Minute, true},
		{apiutil.MaxTTLSeconds, time.Duration(apiutil.MaxTTLSeconds) * time.Second, true},
		{apiutil.MaxTTLSeconds + 1, 0, false},
		{1 << 62, 0, false},
	} {
		got, err := ttlOf(tc.seconds)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ttlOf(%d) = %v, %v; want %v, ok=%v", tc.seconds, got, err, tc.want, tc.ok)
		}
		if err != nil && connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("ttlOf(%d): code %v, want InvalidArgument", tc.seconds, connect.CodeOf(err))
		}
	}
}

// A lifetime a duration cannot hold is refused, naming the limit: it wrapped,
// and was refused as negative or minted with the default lifetime.
func TestIssueRefusesATTLPastWhatADurationHolds(t *testing.T) {
	for _, ttl := range []int64{apiutil.MaxTTLSeconds + 1, 1 << 62} {
		h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
		req := issueRequest(uuid.New())
		req.TtlSeconds = ttl
		_, err := h.Issue(adminCtx(), req)
		if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), strconv.FormatInt(apiutil.MaxTTLSeconds, 10)) {
			t.Errorf("ttl %d: err = %v, want InvalidArgument naming the limit", ttl, err)
		}
	}
}

func TestDelegationRefusesATTLPastWhatADurationHolds(t *testing.T) {
	req := &adminv1.CapabilityServiceDelegateRequest{TtlSeconds: apiutil.MaxTTLSeconds + 1}
	if _, err := delegationTerms(req, limes.Capability{}, limes.AbstractUnitCode); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
}
