package openapi

import (
	"context"
	"fmt"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
)

var (
	spec     *openapi3.T
	initOnce sync.Once
	initErr  error
)

// LoadSpec loads and validates the OpenAPI 3.0 specification from the given path.
// It resolves all references and validates the structure.
// The result is cached, so subsequent calls return the same object (or error).
func LoadSpec(path string) (*openapi3.T, error) {
	initOnce.Do(func() {
		ctx := context.Background()
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true

		doc, err := loader.LoadFromFile(path)
		if err != nil {
			initErr = fmt.Errorf("failed to load openapi spec from %s: %w", path, err)
			return
		}

		if err = doc.Validate(ctx); err != nil {
			initErr = fmt.Errorf("failed to validate openapi spec: %w", err)
			return
		}

		spec = doc
	})

	return spec, initErr
}
