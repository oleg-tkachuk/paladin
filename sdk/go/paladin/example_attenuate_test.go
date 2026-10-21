package paladin_test

import (
	"time"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The README's "Narrowing a capability offline" example: compiled here, so a
// change to Attenuation's fields fails where the README's copy would.
func ExampleAttenuate() {
	var biscuit, dataURL string // from an Issue answer, and the data plane's URL
	narrowed, err := paladin.Attenuate(biscuit, paladin.Attenuation{
		Ops:              []paladin.Op{paladin.OpGet},
		ResourcePrefixes: []string{"corpus/public/"},
		Planes:           []paladin.Plane{paladin.PlaneData},
		ExpiresAt:        time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		return
	}
	_, _ = paladin.New(dataURL, paladin.WithCapability(narrowed))
}
