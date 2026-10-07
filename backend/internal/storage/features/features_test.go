package features_test

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

// every returns a result per catalog feature with support s, then applies the
// overrides.
func every(s features.Support, overrides map[features.Feature]features.Support) []features.Result {
	out := make([]features.Result, 0, len(features.Catalog))
	for _, spec := range features.Catalog {
		got := s
		if o, ok := overrides[spec.Feature]; ok {
			got = o
		}
		out = append(out, features.Result{Feature: spec.Feature, Support: got})
	}
	return out
}

func TestAssess(t *testing.T) {
	for name, tc := range map[string]struct {
		recorded []features.Result
		want     features.Compatibility
	}{
		"never probed": {nil, features.Unverified},
		"every feature supported": {
			every(features.Supported, nil), features.Compatible,
		},
		"an optional feature unsupported": {
			every(features.Supported, map[features.Feature]features.Support{features.AnonymousReadPolicy: features.Unsupported}),
			features.Compatible,
		},
		"a required feature unknown": {
			every(features.Supported, map[features.Feature]features.Support{features.ChecksumSHA256: features.Unknown}),
			features.Unverified,
		},
		"a required feature unsupported": {
			every(features.Supported, map[features.Feature]features.Support{features.ConditionalPut: features.Unsupported}),
			features.Incompatible,
		},
		"unsupported outranks unknown": {
			every(features.Unknown, map[features.Feature]features.Support{features.MultipartUpload: features.Unsupported}),
			features.Incompatible,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := features.Assess(tc.recorded); got != tc.want {
				t.Errorf("Assess = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEveryFeatureListsTheCatalogInOrder(t *testing.T) {
	at := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	recorded := []features.Result{
		{Feature: features.AnonymousReadPolicy, Support: features.Unsupported, Message: "policy not enforced", CheckedAt: at},
		{Feature: "a_feature_since_removed", Support: features.Supported},
	}
	got := features.EveryFeature(recorded)
	if len(got) != len(features.Catalog) {
		t.Fatalf("len = %d, want one per catalog feature (%d)", len(got), len(features.Catalog))
	}
	for i, spec := range features.Catalog {
		if got[i].Feature != spec.Feature {
			t.Errorf("[%d] = %q, want %q", i, got[i].Feature, spec.Feature)
		}
		want := features.Unknown
		if spec.Feature == features.AnonymousReadPolicy {
			want = features.Unsupported
			if got[i].Message != "policy not enforced" || !got[i].CheckedAt.Equal(at) {
				t.Errorf("the recorded result lost its detail: %+v", got[i])
			}
		}
		if got[i].Support != want {
			t.Errorf("%s = %q, want %q", spec.Feature, got[i].Support, want)
		}
	}
}

func TestLookup(t *testing.T) {
	spec, ok := features.Lookup(features.ConditionalPut)
	if !ok || !spec.Required {
		t.Errorf("Lookup(ConditionalPut) = %+v, %v; want a required feature", spec, ok)
	}
	if _, ok := features.Lookup("no_such_feature"); ok {
		t.Error("Lookup found a feature the catalog does not list")
	}
}

func TestSupportOfAnUnrecordedFeatureIsUnknown(t *testing.T) {
	if got := features.SupportOf(nil, features.BucketCreate); got != features.Unknown {
		t.Errorf("SupportOf = %q, want unknown", got)
	}
}
