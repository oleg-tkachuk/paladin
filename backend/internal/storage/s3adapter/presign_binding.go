package s3adapter

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
)

// What a presigned upload is bound to. Each value is signed into the URL (or
// the POST policy), so the object store refuses a request that does not carry
// it exactly — that is what makes the size and checksum Paladin admitted the
// ones that are stored.
const (
	// ifNoneMatchAny makes a PUT conditional on nothing being at the key.
	ifNoneMatchAny = "*"
	// POST policy vocabulary (AWS "Creating a POST policy").
	contentLengthRange = "content-length-range"
	contentTypeField   = "Content-Type"
	contentMD5Field    = "Content-MD5"
	checksumSHA256Form = "x-amz-checksum-sha256"
	checksumCRC32CForm = "x-amz-checksum-crc32c"
	sseField           = "x-amz-server-side-encryption"
	sseKMSKeyField     = "x-amz-server-side-encryption-aws-kms-key-id"
)

// setPutChecksum binds a PUT to its checksum. The value must be set
// explicitly: given only an algorithm, the SDK signs the checksum of the
// empty body it sees at presign time, and every real upload fails.
func setPutChecksum(in *s3.PutObjectInput, algo, value string) error {
	if err := checksum.Validate(algo, value); err != nil {
		return err
	}
	switch strings.ToUpper(algo) {
	case checksum.SHA256:
		in.ChecksumSHA256 = aws.String(value)
	case checksum.CRC32C:
		in.ChecksumCRC32C = aws.String(value)
	case checksum.MD5:
		in.ContentMD5 = aws.String(value)
	}
	return nil
}

// setPartChecksum is setPutChecksum for one part of a multipart upload.
func setPartChecksum(in *s3.UploadPartInput, algo, value string) error {
	if err := checksum.Validate(algo, value); err != nil {
		return err
	}
	switch strings.ToUpper(algo) {
	case checksum.SHA256:
		in.ChecksumSHA256 = aws.String(value)
	case checksum.CRC32C:
		in.ChecksumCRC32C = aws.String(value)
	case checksum.MD5:
		in.ContentMD5 = aws.String(value)
	}
	return nil
}

// postChecksumField is the form field a POST carries the checksum in.
func postChecksumField(algo string) (string, error) {
	switch strings.ToUpper(algo) {
	case checksum.SHA256:
		return checksumSHA256Form, nil
	case checksum.CRC32C:
		return checksumCRC32CForm, nil
	case checksum.MD5:
		return contentMD5Field, nil
	}
	return "", fmt.Errorf("unknown checksum algorithm %q", algo)
}

// multipartChecksumAlgorithm is the algorithm a multipart upload is created
// with, so the store records each part's checksum and checks the list on
// completion. MD5 has no flexible-checksum form: its parts are bound by
// Content-MD5 alone, verified as each part arrives.
func multipartChecksumAlgorithm(algo string) s3types.ChecksumAlgorithm {
	switch strings.ToUpper(algo) {
	case checksum.SHA256:
		return s3types.ChecksumAlgorithmSha256
	case checksum.CRC32C:
		return s3types.ChecksumAlgorithmCrc32c
	}
	return ""
}

// setCompletedPartChecksum carries a part's checksum into the completion
// list, where the store compares it with the one it recorded for the part.
func setCompletedPartChecksum(p *s3types.CompletedPart, algo, value string) {
	switch strings.ToUpper(algo) {
	case checksum.SHA256:
		p.ChecksumSHA256 = aws.String(value)
	case checksum.CRC32C:
		p.ChecksumCRC32C = aws.String(value)
	}
}

// headChecksum is the checksum a HEAD reported under the algorithm the object
// was uploaded with. Picking whichever field happens to be set would report,
// say, a CRC64NVME the store computed by default as the object's SHA-256.
// MD5 has no checksum field — S3 verifies Content-MD5 on arrival and keeps
// no record of it — so it is always "".
func headChecksum(out *s3.HeadObjectOutput, algo string) string {
	switch strings.ToUpper(algo) {
	case checksum.SHA256:
		return aws.ToString(out.ChecksumSHA256)
	case checksum.CRC32C:
		return aws.ToString(out.ChecksumCRC32C)
	}
	return ""
}

// quoteETag puts an ETag in the quoted form If-Match compares against.
func quoteETag(etag string) string {
	if strings.HasPrefix(etag, `"`) {
		return etag
	}
	return `"` + etag + `"`
}

// signedPostExpiry is signedURLExpiry for a presigned POST, whose policy
// expires ttl after the X-Amz-Date form field it carries.
func signedPostExpiry(values map[string]string, ttl time.Duration) (time.Time, error) {
	signedAt, err := time.Parse(amzDateLayout, values[amzDateParam])
	if err != nil {
		return time.Time{}, fmt.Errorf("presigned post %s: %w", amzDateParam, err)
	}
	return signedAt.Add(ttl), nil
}
