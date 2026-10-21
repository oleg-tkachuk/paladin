package paladin

import (
	"reflect"
	"testing"
	"time"

	"github.com/oleg-tkachuk/limes"
)

// Each field of an Attenuation reaches limes as the dimension it narrows,
// and a field left empty narrows nothing.
func TestToLimes(t *testing.T) {
	const (
		requests = int64(25)
		nanos    = int64(1_500_000_000)
	)
	expires := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	r, n := requests, nanos
	got := toLimes(Attenuation{
		Ops: []Op{OpGet}, ResourcePrefixes: []string{"corpus/"}, ResourceURIs: []string{"paladin://x"},
		Planes: []Plane{PlaneData}, ExpiresAt: expires, BindJKT: "jkt", MaxRequests: &r, MaxBudgetNanos: &n,
	})
	want := limes.Attenuation{
		Ops: []limes.Op{limes.OpGet}, ResourcePrefixes: []string{"corpus/"}, ResourceURIs: []string{"paladin://x"},
		Audience: []string{"data"}, ExpiresAt: expires, ConfirmationJKT: "jkt",
		Requests: &limes.RequestCeiling{Max: requests}, Budget: &limes.BudgetCeiling{Max: limes.Nanos(nanos)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toLimes =\n%+v\nwant\n%+v", got, want)
	}
	if got := toLimes(Attenuation{}); !reflect.DeepEqual(got, limes.Attenuation{}) {
		t.Errorf("an empty attenuation narrows %+v", got)
	}
}
