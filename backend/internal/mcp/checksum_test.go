package mcp

import (
	"testing"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// paladin_initiate_multipart_upload sent no checksum algorithm, which the
// request's validation refuses, so the tool never opened an upload. Every
// upload tool now names one, SHA256 when the agent does not.
func TestChecksumAlgorithm(t *testing.T) {
	for in, want := range map[string]commonv1.ChecksumAlgorithm{
		"":       commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		"SHA256": commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		"crc32c": commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C,
		"MD5":    commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5,
	} {
		if got := checksumAlgorithm(in); got != want {
			t.Errorf("checksumAlgorithm(%q) = %v, want %v", in, got, want)
		}
	}
}
