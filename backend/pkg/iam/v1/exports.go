// Package iamv1 re-exports the protobuf-generated types for paladin.iam.v1.
package iamv1

import internal "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"

type (
	User      = internal.User
	TokenPair = internal.TokenPair

	LoginRequest           = internal.LoginRequest
	LoginResponse          = internal.LoginResponse
	RefreshTokenRequest    = internal.RefreshTokenRequest
	RefreshTokenResponse   = internal.RefreshTokenResponse
	RevokeRequest          = internal.RevokeRequest
	RevokeResponse         = internal.RevokeResponse
	WhoAmIRequest          = internal.WhoAmIRequest
	WhoAmIResponse         = internal.WhoAmIResponse
	ChangePasswordRequest  = internal.ChangePasswordRequest
	ChangePasswordResponse = internal.ChangePasswordResponse

	CreateUserRequest     = internal.CreateUserRequest
	GetUserRequest        = internal.GetUserRequest
	UpdateUserRequest     = internal.UpdateUserRequest
	DeleteUserRequest     = internal.DeleteUserRequest
	DeleteUserResponse    = internal.DeleteUserResponse
	ListUsersRequest      = internal.ListUsersRequest
	ListUsersResponse     = internal.ListUsersResponse
	GrantScopesRequest    = internal.GrantScopesRequest
	RevokeScopesRequest   = internal.RevokeScopesRequest
	ResetPasswordRequest  = internal.ResetPasswordRequest
	ResetPasswordResponse = internal.ResetPasswordResponse
)
