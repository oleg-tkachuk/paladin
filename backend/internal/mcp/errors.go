package mcp

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
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
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	plane := catalogAudience[tool]
	switch ce.Code() {
	case connect.CodeUnauthenticated:
		if strings.Contains(ce.Message(), audienceMismatch) && plane != "" {
			return fmt.Errorf("%s calls the %s plane, and this session's token is not issued for %s%s; "+
				"use a token whose audience includes it, such as an API token covering that plane",
				tool, plane, audiencePrefix, plane)
		}
		return errors.New("the access token was refused; it may have expired")
	case connect.CodeUnavailable, connect.CodeInternal, connect.CodeUnknown,
		connect.CodeDeadlineExceeded, connect.CodeDataLoss:
		where := "the platform"
		if plane != "" {
			where = "the " + plane + " plane"
		}
		return fmt.Errorf("%s: %s could not complete the call; try again later", ce.Code(), where)
	default:
		return fmt.Errorf("%s: %s", ce.Code(), ce.Message())
	}
}
