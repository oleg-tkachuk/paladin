//go:build tools

// `--all` was a mockery v2 flag; in v3 the equivalent is `all: true` per
// package in .mockery.yaml, and the config path needs `=`. The stale form
// failed with "unknown command", hidden by the `tools` build tag keeping this
// file out of `go generate ./...`.
//go:generate go run github.com/vektra/mockery/v3 --config=.mockery.yaml

package tools

import (
	_ "github.com/sqlc-dev/sqlc/cmd/sqlc"
	_ "github.com/vektra/mockery/v3"
)
