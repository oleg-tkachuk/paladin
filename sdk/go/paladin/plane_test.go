package paladin_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sharedPlanes is sdk/testdata/planes.json, which the Python SDK and the
// server are held to as well.
const sharedPlanes = "../../testdata/planes.json"

var allPlanes = []paladin.Plane{paladin.PlaneData, paladin.PlaneAdmin, paladin.PlaneIAM, paladin.PlaneMCP}

func TestPlanesMatchTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(sharedPlanes)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Planes map[paladin.Plane]string `json:"planes"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Planes) != len(allPlanes) {
		t.Fatalf("the table names %d planes, the SDK %d", len(table.Planes), len(allPlanes))
	}
	for _, p := range allPlanes {
		audience, ok := table.Planes[p]
		if !ok {
			t.Errorf("plane %q is not in the table", p)
			continue
		}
		if got := p.Audience(); got != audience {
			t.Errorf("%q.Audience() = %q, the table says %q", p, got, audience)
		}
	}
}

// The trap a typed label closes: a label is never its plane's audience.
func TestPlaneIsNotItsAudience(t *testing.T) {
	for _, p := range allPlanes {
		if string(p) == p.Audience() {
			t.Errorf("plane %q is spelt as its audience", p)
		}
	}
	if got := paladin.Plane(paladin.AudienceData).Audience(); got != "" {
		t.Errorf("an audience read as a plane maps to %q, want none", got)
	}
}

func TestPlaneNames(t *testing.T) {
	got := paladin.PlaneNames(paladin.PlaneData, paladin.PlaneMCP)
	if want := []string{"data", "mcp"}; !slices.Equal(got, want) {
		t.Errorf("PlaneNames = %v, want %v", got, want)
	}
	if got := paladin.PlaneNames(); len(got) != 0 {
		t.Errorf("PlaneNames() = %v, want empty", got)
	}
}
