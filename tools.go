//go:build tools
// +build tools

//go:generate go run github.com/vektra/mockery/v2 --all --config .mockery.yaml

package tools

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
	_ "github.com/vektra/mockery/v3"
)
