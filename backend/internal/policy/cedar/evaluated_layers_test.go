package cedar

import (
	"strings"
	"testing"
)

// EvaluatedLayers is the one join of the stored layers: the engine compiles
// what it returns, and GetEffectivePolicy shows it. Order, markers and the
// freeze are what the two must agree on.
func TestEvaluatedLayers(t *testing.T) {
	const tenant, bucket, collection = `permit(principal, action == Action::"GetObject", resource);`,
		`permit(principal, action == Action::"PutObject", resource);`,
		`forbid(principal, action == Action::"DeleteObject", resource);`

	t.Run("every layer, in order", func(t *testing.T) {
		layers, text := EvaluatedLayers(Layers{Tenant: tenant, Bucket: bucket, Collection: collection})
		var names []string
		for _, l := range layers {
			names = append(names, l.Name)
		}
		if strings.Join(names, ",") != "tenant,bucket,collection" {
			t.Fatalf("layers = %v", names)
		}
		want := tenant + "\n" + bucketLayerMarker + "\n" + bucket + "\n" + collectionLayerMarker + "\n" + collection
		if text != want {
			t.Fatalf("text =\n%s\nwant\n%s", text, want)
		}
	})

	t.Run("empty layers are left out, the tenant's kept", func(t *testing.T) {
		layers, text := EvaluatedLayers(Layers{Collection: collection})
		if len(layers) != 2 || layers[0].Name != LayerTenant || layers[1].Name != LayerCollection {
			t.Fatalf("layers = %+v", layers)
		}
		if text != collectionLayerMarker+"\n"+collection {
			t.Fatalf("text = %q", text)
		}
	})

	t.Run("a layer that does not parse is replaced by its freeze", func(t *testing.T) {
		layers, text := EvaluatedLayers(Layers{Tenant: tenant, Bucket: "not cedar"})
		b := layers[1]
		if !b.Frozen || b.Stored != "not cedar" || b.Evaluated != freezeExcept(ActionConfigureBucketPolicy) {
			t.Fatalf("bucket layer = %+v", b)
		}
		if layers[0].Frozen || strings.Contains(text, "not cedar") {
			t.Fatalf("the freeze leaked: %+v / %q", layers[0], text)
		}
	})
}

// What GetEffectivePolicy reports as merged is what compile builds the set
// from.
func TestCompiledTextIsWhatCompileReads(t *testing.T) {
	_, text := EvaluatedLayers(Layers{Tenant: `permit(principal, action, resource);`})
	got := CompiledText(text)
	if !strings.HasPrefix(got, BuiltinPolicy()) || !strings.HasSuffix(got, "\n"+tenantPolicyMarker+"\n"+text) {
		t.Fatalf("compiled text =\n%s", got)
	}
	if _, err := compile(text); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if CompiledText("") != BuiltinPolicy() {
		t.Error("with no tenant text the set is the built-in alone")
	}
}
