package mcp

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// audienceMismatch is how the planes' JWT verifier words a token issued for
// another plane (internal/auth/jwt.go).
const audienceMismatch = "audience mismatch"

// audiencePrefix turns a catalog Audience ("admin") into the token audience
// a plane checks ("paladin-admin").
const audiencePrefix = "paladin-"

// catalogAudience is each tool's plane, from DefaultCatalog.
var catalogAudience = func() map[string]string {
	m := make(map[string]string, len(DefaultCatalog))
	for _, t := range DefaultCatalog {
		m[t.Name] = t.Audience
	}
	return m
}()

// toolError is what a failed tool call tells the model. The raw error reached
// it before: "unauthenticated: jwt: audience mismatch", which says nothing the
// model can act on, and on an outage the upstream's dial error, which names
// internal hosts. Codes that describe the request keep their message — a
// NotFound or a Cedar denial is the answer the model needs.
func toolError(tool string, err error) error {
	plane := catalogAudience[tool]
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	if serverFailure[ce.Code()] {
		where := "the platform"
		if plane != "" {
			where = "the " + plane + " plane"
		}
		return fmt.Errorf("%s: %s could not complete the call; try again later", ce.Code(), where)
	}
	switch ce.Code() {
	case connect.CodeUnauthenticated:
		// The capability library's refusal names what to do — a revoked or
		// expired capability is replaced, not retried — and none of the
		// server's internals.
		if code := limesCodeOf(ce); code != "" {
			return fmt.Errorf("the capability was refused (%s): %s", code, ce.Message())
		}
		if strings.Contains(ce.Message(), audienceMismatch) && plane != "" {
			return fmt.Errorf("%s calls the %s plane, and this session's token is not issued for %s%s; "+
				"use a token whose audience includes it, such as an API token covering that plane",
				tool, plane, audiencePrefix, plane)
		}
		return errors.New("the access token was refused; it may have expired")
	default:
		return fmt.Errorf("%s: %s", ce.Code(), ce.Message())
	}
}

// limesCodeOf is the capability library's code a plane's error carries in
// its Paladin ErrorInfo, empty when it carries none.
func limesCodeOf(ce *connect.Error) string {
	for _, d := range ce.Details() {
		v, err := connectproto.UnmarshalErrorDetail(d)
		if err != nil {
			continue
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == paladin.ErrorDomain {
			return info.GetMetadata()[paladin.ErrorMetadataLimesCode]
		}
	}
	return ""
}
