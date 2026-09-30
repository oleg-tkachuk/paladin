// Package iamv1connect re-exports Connect server/client constructors for
// paladin.iam.v1 services.
package iamv1connect

import internal "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/iam/v1/paladiniamv1connect"

type (
	AuthServiceHandler = internal.AuthServiceHandler
	UserServiceHandler = internal.UserServiceHandler
)

var (
	NewAuthServiceClient = internal.NewAuthServiceClient
	NewUserServiceClient = internal.NewUserServiceClient
)
