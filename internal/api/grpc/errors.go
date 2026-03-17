package grpcapi

import (
	"errors"

	"connectrpc.com/connect"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"google.golang.org/grpc/status"
)

// grpcError converts a domain error (AppError) to a Connect error with
// the correct gRPC status code. Both gRPC and Connect handlers use this
// because connect.NewError is wire-compatible for both protocols.
func grpcError(err error) error {
	grpcErr := apperrors.MapToGRPC(err)
	if s, ok := status.FromError(grpcErr); ok {
		return connect.NewError(connect.Code(s.Code()), errors.New(s.Message()))
	}
	return grpcErr
}
