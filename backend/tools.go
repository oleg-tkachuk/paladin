//go:build tools

//go:generate go run github.com/vektra/mockery/v3 --all --config .mockery.yaml

package tools

import (
	_ "connectrpc.com/connect/cmd/protoc-gen-connect-go"
	_ "github.com/google/wire/cmd/wire"
	_ "github.com/sqlc-dev/sqlc/cmd/sqlc"
	_ "github.com/vektra/mockery/v3"
)
