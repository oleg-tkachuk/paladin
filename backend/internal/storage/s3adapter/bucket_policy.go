package s3adapter

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// anonymousReadPolicySource is the bucket policy granting unsigned
// s3:GetObject under one prefix. A template file rather than a Go structure:
// the rendered document is what the store parses, so it is the thing a test
// should read.
//
//go:embed policies/anonymous_read.json.tmpl
var anonymousReadPolicySource string

var anonymousReadPolicy = template.Must(template.New("anonymous_read").Parse(anonymousReadPolicySource))

// policyBucketName is the S3 bucket-name grammar; policyPrefix is the
// characters a prefix Paladin writes into a policy may hold. Both are checked
// before rendering, so nothing that could close a JSON string or widen the
// ARN reaches the template.
var (
	policyBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	policyPrefix     = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
)

// anonymousReadScope is the template's input.
type anonymousReadScope struct {
	Bucket string
	// Prefix ends in "/" or is empty, which is the whole bucket.
	Prefix string
}

// renderAnonymousReadPolicy renders the policy granting anonymous reads of
// every object under prefix in bucket. An empty prefix grants the bucket.
func renderAnonymousReadPolicy(bucket, prefix string) (string, error) {
	if !policyBucketName.MatchString(bucket) {
		return "", fmt.Errorf("bucket policy: %q is not a bucket name", bucket)
	}
	if !policyPrefix.MatchString(prefix) || strings.Contains(prefix, "*") {
		return "", fmt.Errorf("bucket policy: prefix %q holds a character a policy must not", prefix)
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		return "", fmt.Errorf("bucket policy: prefix %q must end in /", prefix)
	}
	var b strings.Builder
	if err := anonymousReadPolicy.Execute(&b, anonymousReadScope{Bucket: bucket, Prefix: prefix}); err != nil {
		return "", fmt.Errorf("bucket policy: %w", err)
	}
	if !json.Valid([]byte(b.String())) {
		return "", fmt.Errorf("bucket policy: rendered an invalid document for %q", bucket)
	}
	return b.String(), nil
}
