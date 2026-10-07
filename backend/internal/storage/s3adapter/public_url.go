package s3adapter

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// PublicURL is where an object in a public bucket is read unsigned
// (ADR-0027). Under publicBaseURL — a CDN serving the bucket at its root —
// when one is set; otherwise at the backend's public endpoint (its internal
// one when it has none), addressed path- or host-style as presigned URLs
// are, by the SDK's own endpoint resolver.
func (c *Client) PublicURL(ctx context.Context, bucket, publicBaseURL string, tenantID uuid.UUID, collection, key string) (string, error) {
	segments := strings.Split(composeKey(tenantID, collection, key), "/")
	if publicBaseURL != "" {
		base, err := url.Parse(publicBaseURL)
		if err != nil {
			return "", fmt.Errorf("public base url %q: %w", publicBaseURL, err)
		}
		return base.JoinPath(segments...).String(), nil
	}
	params := s3.EndpointParameters{
		Bucket:         aws.String(c.resolveBucket(bucket)),
		Region:         aws.String(c.cfg.Region),
		ForcePathStyle: aws.Bool(c.cfg.ForcePathStyle),
	}
	endpoint := c.cfg.PublicEndpoint
	if endpoint == "" {
		endpoint = c.cfg.Endpoint
	}
	if endpoint != "" {
		params.Endpoint = aws.String(endpoint)
	}
	resolved, err := s3.NewDefaultEndpointResolverV2().ResolveEndpoint(ctx, params)
	if err != nil {
		return "", fmt.Errorf("resolve the public endpoint of bucket %q: %w", bucket, err)
	}
	return resolved.URI.JoinPath(segments...).String(), nil
}
