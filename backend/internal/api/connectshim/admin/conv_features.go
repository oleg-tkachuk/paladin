package admin

import (
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// featureProto maps the catalog onto the proto enum. Every catalog feature
// has an entry and every non-zero enum value is reached; a test holds both.
var featureProto = map[features.Feature]pb.StorageFeature{
	features.ConditionalPut:      pb.StorageFeature_STORAGE_FEATURE_CONDITIONAL_PUT,
	features.ChecksumSHA256:      pb.StorageFeature_STORAGE_FEATURE_CHECKSUM_SHA256,
	features.MultipartUpload:     pb.StorageFeature_STORAGE_FEATURE_MULTIPART_UPLOAD,
	features.ServerSideCopy:      pb.StorageFeature_STORAGE_FEATURE_SERVER_SIDE_COPY,
	features.PresignedPost:       pb.StorageFeature_STORAGE_FEATURE_PRESIGNED_POST,
	features.BucketCreate:        pb.StorageFeature_STORAGE_FEATURE_BUCKET_CREATE,
	features.AnonymousReadPolicy: pb.StorageFeature_STORAGE_FEATURE_ANONYMOUS_READ_POLICY,
}

var supportProto = map[features.Support]pb.FeatureSupport{
	features.Supported:   pb.FeatureSupport_FEATURE_SUPPORT_SUPPORTED,
	features.Unsupported: pb.FeatureSupport_FEATURE_SUPPORT_UNSUPPORTED,
	features.Unknown:     pb.FeatureSupport_FEATURE_SUPPORT_UNKNOWN,
}

var compatibilityProto = map[features.Compatibility]pb.StorageCompatibility{
	features.Unverified:   pb.StorageCompatibility_STORAGE_COMPATIBILITY_UNVERIFIED,
	features.Compatible:   pb.StorageCompatibility_STORAGE_COMPATIBILITY_COMPATIBLE,
	features.Incompatible: pb.StorageCompatibility_STORAGE_COMPATIBILITY_INCOMPATIBLE,
}

// featuresToProto lists one entry per catalog feature, Unknown where nothing
// was recorded, with each feature's requirement and purpose from the catalog.
func featuresToProto(recorded []features.Result) []*pb.StorageFeatureSupport {
	complete := features.EveryFeature(recorded)
	out := make([]*pb.StorageFeatureSupport, 0, len(complete))
	for _, r := range complete {
		spec, _ := features.Lookup(r.Feature)
		out = append(out, &pb.StorageFeatureSupport{
			Feature:   featureProto[r.Feature],
			Support:   supportProto[r.Support],
			Required:  spec.Required,
			Enables:   spec.Enables,
			Message:   r.Message,
			CheckedAt: convx.TsProto(r.CheckedAt),
		})
	}
	return out
}

func compatibilityToProto(recorded []features.Result) pb.StorageCompatibility {
	return compatibilityProto[features.Assess(recorded)]
}
