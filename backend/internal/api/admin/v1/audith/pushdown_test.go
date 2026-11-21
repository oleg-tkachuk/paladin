package audith

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// The pushdown fills only what the request left open: a predicate the caller
// set is never replaced by one the filter implies.
func TestApplyAuditPushdown(t *testing.T) {
	lo := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	hi := lo.Add(24 * time.Hour)
	const everything = `actor_subject == "alice" && action == "/a.B/C" && action.startsWith("/a.") && ` +
		`at >= timestamp("2026-05-01T12:00:00Z") && at <= timestamp("2026-05-02T12:00:00Z")`
	preset := admindomain.ListAuditArgs{
		ActorSubject: "bob", ActionEq: "/x.Y/Z", ActionPrefix: "/x.",
		AtGTE: lo.Add(time.Hour), AtLTE: hi.Add(time.Hour),
	}
	for _, tc := range []struct {
		name   string
		args   admindomain.ListAuditArgs
		filter string
		want   admindomain.ListAuditArgs
	}{
		{"no filter", admindomain.ListAuditArgs{}, "", admindomain.ListAuditArgs{}},
		{"unparsable filter", admindomain.ListAuditArgs{}, "((", admindomain.ListAuditArgs{}},
		{"fills every open predicate", admindomain.ListAuditArgs{}, everything, admindomain.ListAuditArgs{
			ActorSubject: "alice", ActionEq: "/a.B/C", ActionPrefix: "/a.", AtGTE: lo, AtLTE: hi,
		}},
		{"keeps what the caller set", preset, everything, preset},
		{"leaves open what the filter does not name", admindomain.ListAuditArgs{}, `resource_name == "x"`, admindomain.ListAuditArgs{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.args
			applyAuditPushdown(&got, tc.filter)
			if got.ActorSubject != tc.want.ActorSubject || got.ActionEq != tc.want.ActionEq ||
				got.ActionPrefix != tc.want.ActionPrefix || !got.AtGTE.Equal(tc.want.AtGTE) || !got.AtLTE.Equal(tc.want.AtLTE) {
				t.Errorf("args = %+v, want %+v", got, tc.want)
			}
		})
	}
}
