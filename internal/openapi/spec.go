package openapi

import (
	"fmt"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oleg-tkachuk/paladin/internal/generated/api"
)

var (
	spec     *openapi3.T
	initOnce sync.Once
	initErr  error
)

// LoadSpec returns the validated OpenAPI 3.0 specification from the generated code.
// The result is cached, so subsequent calls return the same object (or error).
func LoadSpec() (*openapi3.T, error) {
	initOnce.Do(func() {
		doc, err := api.GetSwagger()
		if err != nil {
			initErr = fmt.Errorf("failed to get swagger spec: %w", err)
			return
		}

		spec = doc
	})

	return spec, initErr
}
