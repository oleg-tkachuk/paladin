package admin

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The catalog and the proto enum name the same features: a feature added to
// one alone would reach the console as UNSPECIFIED, or never be probed.
func TestTheFeatureEnumMirrorsTheCatalog(t *testing.T) {
	reached := map[pb.StorageFeature]bool{}
	for _, spec := range features.Catalog {
		v, ok := featureProto[spec.Feature]
		if !ok || v == pb.StorageFeature_STORAGE_FEATURE_UNSPECIFIED {
			t.Errorf("catalog feature %s has no proto value", spec.Feature)
		}
		reached[v] = true
	}
	for v := range pb.StorageFeature_name {
		f := pb.StorageFeature(v)
		if f != pb.StorageFeature_STORAGE_FEATURE_UNSPECIFIED && !reached[f] {
			t.Errorf("proto value %s is not in the catalog", f)
		}
	}
	for _, s := range []features.Support{features.Supported, features.Unsupported, features.Unknown} {
		if supportProto[s] == pb.FeatureSupport_FEATURE_SUPPORT_UNSPECIFIED {
			t.Errorf("support %s has no proto value", s)
		}
	}
	for _, c := range []features.Compatibility{features.Unverified, features.Compatible, features.Incompatible} {
		if compatibilityProto[c] == pb.StorageCompatibility_STORAGE_COMPATIBILITY_UNSPECIFIED {
			t.Errorf("compatibility %s has no proto value", c)
		}
	}
}

func TestFeaturesToProto(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	recorded := []features.Result{
		{Feature: features.ConditionalPut, Support: features.Unsupported, Message: "replaced", CheckedAt: at},
	}
	got := featuresToProto(recorded)
	if len(got) != len(features.Catalog) {
		t.Fatalf("%d entries, want one per catalog feature", len(got))
	}
	first := got[0]
	if first.GetFeature() != pb.StorageFeature_STORAGE_FEATURE_CONDITIONAL_PUT ||
		first.GetSupport() != pb.FeatureSupport_FEATURE_SUPPORT_UNSUPPORTED ||
		!first.GetRequired() || first.GetEnables() == "" ||
		first.GetMessage() != "replaced" || !first.GetCheckedAt().AsTime().Equal(at) {
		t.Errorf("recorded entry = %v", first)
	}
	last := got[len(got)-1]
	if last.GetSupport() != pb.FeatureSupport_FEATURE_SUPPORT_UNKNOWN || last.GetRequired() || last.GetCheckedAt() != nil {
		t.Errorf("never-probed optional entry = %v, want unknown, optional, no checked_at", last)
	}
	if c := compatibilityToProto(recorded); c != pb.StorageCompatibility_STORAGE_COMPATIBILITY_INCOMPATIBLE {
		t.Errorf("compatibility = %s, want incompatible", c)
	}
	if c := compatibilityToProto(nil); c != pb.StorageCompatibility_STORAGE_COMPATIBILITY_UNVERIFIED {
		t.Errorf("never probed = %s, want unverified", c)
	}
}
